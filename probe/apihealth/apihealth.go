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
		return false, fmt.Sprintf("HTTP status %d, expected 200", resp.StatusCode)
	}

	var health healthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return false, fmt.Sprintf("Failed to parse JSON: %v", err)
	}

	var errors []string

	if health.Status != "ok" {
		errors = append(errors, fmt.Sprintf("status=%q, expected \"ok\"", health.Status))
	}
	if !health.DatasourceStatus.Enabled {
		errors = append(errors, "datasource not enabled")
	}

	now := time.Now().UTC()

	for tile, dateHour := range health.DatasourceStatus.TileDetails {
		if err := a.checkFreshness(now, dateHour, fmt.Sprintf("tile_details[%s]", tile)); err != nil {
			errors = append(errors, err.Error())
		}
	}

	if len(health.DatasourceStatus.GeojsonDateHours) > 0 {
		latest := health.DatasourceStatus.GeojsonDateHours[0]
		if err := a.checkFreshness(now, latest, "geojson_date_hours"); err != nil {
			errors = append(errors, err.Error())
		}
	} else {
		errors = append(errors, "geojson_date_hours is empty")
	}

	for model, hours := range health.DatasourceStatus.ForecastDetails {
		if len(hours) > 0 {
			if err := a.checkFreshness(now, hours[0], fmt.Sprintf("forecast[%s]", model)); err != nil {
				errors = append(errors, err.Error())
			}
		} else {
			errors = append(errors, fmt.Sprintf("forecast[%s] is empty", model))
		}
	}

	if len(errors) > 0 {
		return false, strings.Join(errors, "; ")
	}

	return true, "API health check passed"
}

// checkFreshness verifies a date_hour string is within MaxAge of now.
func (a *APIHealth) checkFreshness(now time.Time, dateHour, label string) error {
	t, err := parseDateHour(dateHour)
	if err != nil {
		return fmt.Errorf("%s: invalid date_hour %q: %v", label, dateHour, err)
	}
	age := now.Sub(t)
	if age > a.MaxAge {
		return fmt.Errorf("%s: %s is %s old (max %s)", label, dateHour, age.Round(time.Minute), a.MaxAge)
	}
	return nil
}

// parseDateHour parses a date_hour string like "20260312_18" into a UTC time.
func parseDateHour(s string) (time.Time, error) {
	return time.ParseInLocation("20060102_15", s, time.UTC)
}
