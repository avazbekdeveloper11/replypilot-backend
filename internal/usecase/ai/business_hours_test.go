package ai

import (
	"testing"
	"time"

	"github.com/replypilot/backend/internal/domain/entity"
)

func minutesPtr(m int) *int { return &m }

func TestWithinBusinessHoursDisabled(t *testing.T) {
	org := &entity.Organization{Timezone: "UTC", BusinessHoursEnabled: false}
	if !withinBusinessHours(org, time.Now()) {
		t.Error("expected true (no gating) when business hours disabled")
	}
}

func TestWithinBusinessHoursNotConfigured(t *testing.T) {
	org := &entity.Organization{Timezone: "UTC", BusinessHoursEnabled: true}
	if !withinBusinessHours(org, time.Now()) {
		t.Error("expected true (no gating) when start/end minutes are nil")
	}
}

func TestWithinBusinessHoursBadTimezone(t *testing.T) {
	org := &entity.Organization{
		Timezone:                  "Not/AZone",
		BusinessHoursEnabled:      true,
		BusinessHoursStartMinutes: minutesPtr(9 * 60),
		BusinessHoursEndMinutes:   minutesPtr(18 * 60),
	}
	if !withinBusinessHours(org, time.Now()) {
		t.Error("expected true (fail open) when timezone is unparseable")
	}
}

func TestWithinBusinessHoursSameDayWindow(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	org := &entity.Organization{
		Timezone:                  "Asia/Tashkent",
		BusinessHoursEnabled:      true,
		BusinessHoursStartMinutes: minutesPtr(9 * 60),  // 09:00
		BusinessHoursEndMinutes:   minutesPtr(18 * 60), // 18:00
	}

	inside := time.Date(2026, 8, 8, 12, 0, 0, 0, loc)
	if !withinBusinessHours(org, inside) {
		t.Error("expected true at 12:00 within 09:00-18:00")
	}

	before := time.Date(2026, 8, 8, 8, 0, 0, 0, loc)
	if withinBusinessHours(org, before) {
		t.Error("expected false at 08:00 before 09:00-18:00")
	}

	atStart := time.Date(2026, 8, 8, 9, 0, 0, 0, loc)
	if !withinBusinessHours(org, atStart) {
		t.Error("expected true at exactly the start boundary (inclusive)")
	}

	atEnd := time.Date(2026, 8, 8, 18, 0, 0, 0, loc)
	if withinBusinessHours(org, atEnd) {
		t.Error("expected false at exactly the end boundary (exclusive)")
	}
}

func TestWithinBusinessHoursOvernightWindow(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	org := &entity.Organization{
		Timezone:                  "Asia/Tashkent",
		BusinessHoursEnabled:      true,
		BusinessHoursStartMinutes: minutesPtr(22 * 60), // 22:00
		BusinessHoursEndMinutes:   minutesPtr(6 * 60),  // 06:00
	}

	lateNight := time.Date(2026, 8, 8, 23, 0, 0, 0, loc)
	if !withinBusinessHours(org, lateNight) {
		t.Error("expected true at 23:00 within overnight 22:00-06:00")
	}

	earlyMorning := time.Date(2026, 8, 8, 3, 0, 0, 0, loc)
	if !withinBusinessHours(org, earlyMorning) {
		t.Error("expected true at 03:00 within overnight 22:00-06:00")
	}

	midday := time.Date(2026, 8, 8, 12, 0, 0, 0, loc)
	if withinBusinessHours(org, midday) {
		t.Error("expected false at 12:00 outside overnight 22:00-06:00")
	}
}

func TestWithinBusinessHoursDegenerateEqualEndpoints(t *testing.T) {
	org := &entity.Organization{
		Timezone:                  "UTC",
		BusinessHoursEnabled:      true,
		BusinessHoursStartMinutes: minutesPtr(9 * 60),
		BusinessHoursEndMinutes:   minutesPtr(9 * 60),
	}
	if !withinBusinessHours(org, time.Now()) {
		t.Error("expected true (fail open) when start == end")
	}
}
