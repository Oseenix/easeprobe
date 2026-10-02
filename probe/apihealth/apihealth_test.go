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
	"testing"
	"time"

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
