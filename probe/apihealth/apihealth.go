/*
 * Copyright (c) 2022, MegaEase
 * All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package apihealth is the API health probe package.
package apihealth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/megaease/easeprobe/global"
	"github.com/megaease/easeprobe/probe/base"
	log "github.com/sirupsen/logrus"
)

const defaultMaxAge = 8 * time.Hour

// APIHealth implements a config for API Health checking.
type APIHealth struct {
	base.DefaultProbe `yaml:",inline"`
	URL               string        `yaml:"url" json:"url" jsonschema:"required,format=uri,title=Health URL,description=The health check API URL"`
	MaxAge            time.Duration `yaml:"max_age,omitempty" json:"max_age,omitempty" jsonschema:"type=string,format=duration,title=Max Data Age,description=Maximum allowed age of data timestamps,default=8h"`
}

// healthResponse is the JSON structure of the health API response.
type healthResponse struct {
	Status           string           `json:"status"`
	DatasourceStatus datasourceStatus `json:"datasource_status"`
}

// datasourceStatus is the datasource status part of the health response.
type datasourceStatus struct {
	Enabled          bool                `json:"enabled"`
	Ready            bool                `json:"ready"`
	Tiles            int                 `json:"tiles"`
	Geojson          int                 `json:"geojson"`
	Forecasts        int                 `json:"forecasts"`
	TideModels       []string            `json:"tide_models"`
	TileDetails      map[string]string   `json:"tile_details"`
	GeojsonDateHours []string            `json:"geojson_date_hours"`
	ForecastDetails  map[string][]string `json:"forecast_details"`
}

// Config configures the API Health probe.
func (a *APIHealth) Config(gConf global.ProbeSettings) error {
	kind := "apihealth"
	tag := ""
	name := a.ProbeName
	a.DefaultProbe.Config(gConf, kind, tag, name, a.URL, a.DoProbe)

	if strings.TrimSpace(a.URL) == "" {
		return fmt.Errorf("url is required")
	}

	if a.MaxAge <= 0 {
		a.MaxAge = defaultMaxAge
	}

	log.Debugf("[%s / %s] configuration: url=%s, max_age=%s", a.ProbeKind, a.ProbeName, a.URL, a.MaxAge)
	return nil
}

// DoProbe performs the API health check.
func (a *APIHealth) DoProbe() (bool, string) {
	client := &http.Client{Timeout: a.Timeout()}

	resp, err := client.Get(a.URL)
	if err != nil {
		return false, fmt.Sprintf("HTTP request error: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Sprintf("Failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d (expected 200)", resp.StatusCode)
	}

	var health healthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return false, fmt.Sprintf("Invalid JSON: %v", err)
	}

	var issues []string

	if health.Status != "ok" {
		issues = append(issues, fmt.Sprintf("status=%q (expected \"ok\")", health.Status))
	}
	if !health.DatasourceStatus.Enabled {
		issues = append(issues, "datasource not enabled")
	}

	now := time.Now().UTC()
	if msg := a.checkAllFreshness(now, &health.DatasourceStatus); msg != "" {
		issues = append(issues, msg)
	}

	if len(issues) > 0 {
		return false, strings.Join(issues, "; ")
	}
	return true, "All checks passed"
}

type staleGroup struct {
	category string
	items    []string
	dateHour string
	age      time.Duration
}

func (a *APIHealth) checkAllFreshness(now time.Time, ds *datasourceStatus) string {
	var groups []staleGroup
	var missing []string
	var badFormat []string

	if len(ds.TileDetails) == 0 {
		missing = append(missing, "tiles")
	} else {
		var names []string
		var dh string
		var elapsed time.Duration
		for tile, dateHour := range ds.TileDetails {
			t, err := parseDateHour(dateHour)
			if err != nil {
				badFormat = append(badFormat, fmt.Sprintf("tile[%s]=%q", tile, dateHour))
				continue
			}
			elapsed = now.Sub(t)
			if elapsed > a.MaxAge {
				names = append(names, tile)
				dh = dateHour
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			groups = append(groups, staleGroup{"tiles", names, dh, elapsed})
		}
	}

	if len(ds.GeojsonDateHours) == 0 {
		missing = append(missing, "geojson")
	} else {
		latest := ds.GeojsonDateHours[0]
		t, err := parseDateHour(latest)
		if err != nil {
			badFormat = append(badFormat, fmt.Sprintf("geojson=%q", latest))
		} else if elapsed := now.Sub(t); elapsed > a.MaxAge {
			groups = append(groups, staleGroup{"geojson", nil, latest, elapsed})
		}
	}

	if len(ds.ForecastDetails) == 0 {
		missing = append(missing, "forecast")
	} else {
		var names []string
		var dh string
		var elapsed time.Duration
		for model, hours := range ds.ForecastDetails {
			if len(hours) == 0 {
				missing = append(missing, fmt.Sprintf("forecast[%s]", model))
				continue
			}
			t, err := parseDateHour(hours[0])
			if err != nil {
				badFormat = append(badFormat, fmt.Sprintf("forecast[%s]=%q", model, hours[0]))
				continue
			}
			elapsed = now.Sub(t)
			if elapsed > a.MaxAge {
				names = append(names, model)
				dh = hours[0]
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			groups = append(groups, staleGroup{"forecast", names, dh, elapsed})
		}
	}

	var lines []string

	if len(groups) > 0 {
		allSameDH := true
		for _, g := range groups[1:] {
			if g.dateHour != groups[0].dateHour {
				allSameDH = false
				break
			}
		}

		if allSameDH {
			lines = append(lines, fmt.Sprintf("Stale data (max %s) — %s (%s old)",
				fmtDuration(a.MaxAge), groups[0].dateHour, fmtDuration(groups[0].age)))
			for _, g := range groups {
				lines = append(lines, "- "+formatGroupLabel(g))
			}
		} else {
			lines = append(lines, fmt.Sprintf("Stale data (max %s)", fmtDuration(a.MaxAge)))
			for _, g := range groups {
				lines = append(lines, fmt.Sprintf("- %s @ %s (%s old)",
					formatGroupLabel(g), g.dateHour, fmtDuration(g.age)))
			}
		}
	}

	if len(missing) > 0 {
		lines = append(lines, "Missing: "+strings.Join(missing, ", "))
	}
	if len(badFormat) > 0 {
		lines = append(lines, "Bad format: "+strings.Join(badFormat, ", "))
	}

	return strings.Join(lines, "\n")
}

func formatGroupLabel(g staleGroup) string {
	if len(g.items) == 0 {
		return g.category
	}
	return fmt.Sprintf("%s[%s]", g.category, strings.Join(g.items, ", "))
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// parseDateHour parses a date_hour string like "20260312_18" into a UTC time.
func parseDateHour(s string) (time.Time, error) {
	return time.ParseInLocation("20060102_15", s, time.UTC)
}
