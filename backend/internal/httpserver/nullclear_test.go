package httpserver_test

import (
	"net/http"
	"testing"
)

// Explicit JSON null clears a nullable patch field. encoding/json sets a
// *json.RawMessage to nil for a literal null, which once made "clear it" read
// as "leave it alone" on these three routes.
func TestPatchNullClearsNullableFields(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("NullClear", "nullclear@jjne.test", "0726000100", []string{"Room 1"}, 200_000)
	c := fix.client

	// Property lat / lng / notes.
	c.do(http.MethodPatch, "/properties/"+fix.propertyID, map[string]any{
		"lat": -6.8, "lng": 39.2, "notes": "gate code 1234",
	}).mustStatus(t, http.StatusOK, "set property fields")
	cleared := c.do(http.MethodPatch, "/properties/"+fix.propertyID, map[string]any{
		"lat": nil, "lng": nil, "notes": nil,
	}).mustStatus(t, http.StatusOK, "clear property fields")
	prop, _ := cleared.Body["property"].(map[string]any)
	for _, k := range []string{"lat", "lng", "notes"} {
		if prop[k] != nil {
			t.Errorf("property %s = %v after null, want null", k, prop[k])
		}
	}

	// Org settings due_day.
	c.do(http.MethodPatch, "/org", map[string]any{"settings": map[string]any{"due_day": 5}}).
		mustStatus(t, http.StatusOK, "set due day")
	org := c.do(http.MethodPatch, "/org", map[string]any{"settings": map[string]any{"due_day": nil}}).
		mustStatus(t, http.StatusOK, "clear due day")
	settings, _ := org.Body["org"].(map[string]any)["settings"].(map[string]any)
	if settings["due_day"] != nil {
		t.Errorf("settings.due_day = %v after null, want null", settings["due_day"])
	}

	// Unit allowed_period_ids: null means every org period is offered again.
	period := c.periodIDByDays(t, 30)
	c.do(http.MethodPatch, "/units/"+fix.unitIDs[0], map[string]any{"allowed_period_ids": []string{period}}).
		mustStatus(t, http.StatusOK, "restrict periods")
	unit := c.do(http.MethodPatch, "/units/"+fix.unitIDs[0], map[string]any{"allowed_period_ids": nil}).
		mustStatus(t, http.StatusOK, "offer every period")
	if got := unit.Body["unit"].(map[string]any)["allowed_period_ids"]; got != nil {
		t.Errorf("allowed_period_ids = %v after null, want null", got)
	}
}
