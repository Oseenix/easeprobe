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

package apihealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/megaease/easeprobe/global"
	"github.com/stretchr/testify/assert"
)

func TestParseTileDetailBareDateHour(t *testing.T) {
	got, err := parseTileDetail("20260312_18")
	assert.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 12, 18, 0, 0, 0, time.UTC), got)
}

func TestParseTileDetailSingleModelSuffix(t *testing.T) {
	got, err := parseTileDetail("20260312_18(wind)")
	assert.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 12, 18, 0, 0, 0, time.UTC), got)
}

func TestParseTileDetailMultiModelPicksOldest(t *testing.T) {
	got, err := parseTileDetail("20260312_18(wave_world),20260312_00(nz),20260312_06(au_east)")
	assert.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC), got)
}

func TestParseTileDetailBadFormat(t *testing.T) {
	_, err := parseTileDetail("not-a-date(nz)")
	assert.Error(t, err)
}

func TestParseTileDetailEmpty(t *testing.T) {
	_, err := parseTileDetail("")
	assert.Error(t, err)
}

func TestDoProbeForecastSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
		want   string
	}{
		{name: "TileDB without legacy forecasts"},
		{name: "missing TileDB", change: func(h map[string]interface{}) { delete(h, "tiledb") }, want: "Missing: tiledb"},
		{name: "TileDB not ready", change: func(h map[string]interface{}) {
			h["tiledb"].(map[string]interface{})["ready"] = false
		}, want: "tiledb not ready"},
		{name: "missing boat", change: func(h map[string]interface{}) {
			delete(h["tiledb"].(map[string]interface{})["sync"].(map[string]interface{})["categories"].(map[string]interface{}), "boat")
		}, want: "Missing: forecast[boat]"},
		{name: "unsynced boat", change: func(h map[string]interface{}) {
			forecastCategory(h, "boat")["synced"] = false
		}, want: "forecast[boat] not synced"},
		{name: "missing array path", change: func(h map[string]interface{}) {
			forecastCategory(h, "surf")["localArrayPath"] = ""
		}, want: "Missing: forecast[surf] array path"},
		{name: "missing active URI", change: func(h map[string]interface{}) {
			delete(h["tiledb"].(map[string]interface{})["uris"].(map[string]interface{}), "surf")
		}, want: "Missing: forecast[surf] array path"},
		{name: "missing timestamp", change: func(h map[string]interface{}) {
			delete(forecastCategory(h, "surf"), "updatedAt")
		}, want: "Missing: forecast[surf] updatedAt"},
		{name: "invalid timestamp", change: func(h map[string]interface{}) {
			forecastCategory(h, "surf")["updatedAt"] = "invalid"
		}, want: "Bad format: forecast[surf]"},
		{name: "stale despite server freshness flag", change: func(h map[string]interface{}) {
			forecastCategory(h, "boat")["updatedAt"] = time.Now().UTC().Add(-16 * time.Hour).Format(time.RFC3339Nano)
			h["forecast_freshness"] = map[string]interface{}{"boat": map[string]interface{}{"stale": false}}
		}, want: "Stale data (max 15h)"},
		{name: "legacy missing forecast", change: func(h map[string]interface{}) {
			delete(h, "data_source")
		}, want: "Missing: forecast"},
		{name: "legacy valid forecast", change: func(h map[string]interface{}) {
			delete(h, "data_source")
			delete(h, "tiledb")
			h["datasource_status"].(map[string]interface{})["forecast_details"] = map[string][]string{
				"wave": {time.Now().UTC().Format("20060102_15")},
			}
		}},
		{name: "legacy stale forecast", change: func(h map[string]interface{}) {
			h["data_source"] = map[string]string{"forecast_primary": "postgres"}
			h["datasource_status"].(map[string]interface{})["forecast_details"] = map[string][]string{
				"wave": {time.Now().UTC().Add(-16 * time.Hour).Format("20060102_15")},
			}
		}, want: "Stale data (max 15h)"},
		{name: "TileDB does not hide missing tiles", change: func(h map[string]interface{}) {
			delete(h["datasource_status"].(map[string]interface{}), "tile_details")
		}, want: "Missing: tiles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			h := map[string]interface{}{
				"status":      "ok",
				"data_source": map[string]string{"forecast_primary": "tiledb"},
				"datasource_status": map[string]interface{}{
					"enabled": true, "ready": true,
					"tile_details":       map[string]string{"wave": now.Format("20060102_15")},
					"geojson_date_hours": []string{now.Format("20060102_15")},
					"forecast_details":   map[string][]string{},
				},
				"tiledb": map[string]interface{}{
					"ready": true,
					"uris":  map[string]interface{}{"surf": "/surf.tdb", "boat": "/boat.tdb"},
					"sync": map[string]interface{}{"categories": map[string]interface{}{
						"surf": map[string]interface{}{"synced": true, "localArrayPath": "/surf.tdb", "updatedAt": now.Format(time.RFC3339Nano)},
						"boat": map[string]interface{}{"synced": true, "localArrayPath": "/boat.tdb", "updatedAt": now.Format(time.RFC3339Nano)},
					}},
				},
			}
			if tc.change != nil {
				tc.change(h)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := json.NewEncoder(w).Encode(h); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			p := APIHealth{URL: server.URL, MaxAge: 15 * time.Hour}
			if !assert.NoError(t, p.Config(global.ProbeSettings{Timeout: time.Second})) {
				return
			}
			ok, message := p.DoProbe()
			assert.Equal(t, tc.want == "", ok, message)
			if tc.want == "" {
				assert.Equal(t, "All checks passed", message)
			} else {
				assert.Contains(t, message, tc.want)
			}
		})
	}
}

func forecastCategory(h map[string]interface{}, category string) map[string]interface{} {
	return h["tiledb"].(map[string]interface{})["sync"].(map[string]interface{})["categories"].(map[string]interface{})[category].(map[string]interface{})
}
