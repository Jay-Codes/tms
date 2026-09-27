package httpserver_test

import (
	"net/http"
	"testing"
)

// Phase 22 §22.1 — template assignment. The rule under test is the resolution
// order: named at approval → the unit's → the property's → the org default,
// and that the contract is actually written on the template it resolved.

func newTemplate(t *testing.T, c *client, name string) string {
	t.Helper()
	return c.do(http.MethodPost, "/contract-templates", map[string]any{
		"name": name, "body_html": "<h1>" + name + "</h1><p>Rent {{rent}} for {{unit}}.</p>",
	}).mustStatus(t, http.StatusCreated, "create template "+name).str(t, "template", "id")
}

func resolvedTemplate(t *testing.T, c *client, unitID string) (id, source string) {
	t.Helper()
	r := c.do(http.MethodGet, "/units/"+unitID+"/template", nil).mustStatus(t, http.StatusOK, "unit template")
	return r.str(t, "template", "id"), r.str(t, "template", "source")
}

func TestPhase22TemplateResolutionOrder(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("TplOrder", "tplorder@jjne.test", "0722000100",
		[]string{"Shop 1", "Flat 1"}, 300_000)
	c := fix.client
	shop := newTemplate(t, c, "Commercial lease")
	block := newTemplate(t, c, "Block A agreement")

	if _, src := resolvedTemplate(t, c, fix.unitIDs[0]); src != "default" {
		t.Fatalf("fresh unit source = %q, want default", src)
	}

	c.do(http.MethodPut, "/properties/"+fix.propertyID+"/template", map[string]any{"template_id": block}).
		mustStatus(t, http.StatusOK, "property template")
	if id, src := resolvedTemplate(t, c, fix.unitIDs[1]); id != block || src != "property" {
		t.Errorf("flat resolves %s/%s, want the property's", id, src)
	}

	c.do(http.MethodPost, "/units/bulk-template", map[string]any{
		"unit_ids": []string{fix.unitIDs[0]}, "template_id": shop,
	}).mustStatus(t, http.StatusOK, "unit template")
	if id, src := resolvedTemplate(t, c, fix.unitIDs[0]); id != shop || src != "unit" {
		t.Errorf("shop resolves %s/%s, want its own", id, src)
	}

	// An assigned template cannot be deleted from under its units.
	del := c.do(http.MethodDelete, "/contract-templates/"+shop, nil)
	if del.Code != http.StatusConflict || del.str(t, "type") != "template_in_use" {
		t.Errorf("delete assigned template = %d %s, want 409 template_in_use", del.Code, del.Raw)
	}
	for _, it := range listOf(t, c.do(http.MethodGet, "/contract-templates", nil).
		mustStatus(t, http.StatusOK, "templates")) {
		if it["id"] == shop {
			usage, _ := it["usage"].(map[string]any)
			if usage["units"] != float64(1) {
				t.Errorf("shop usage = %v, want 1 unit", usage)
			}
		}
	}

	// Clearing goes back through the chain: the property's, then the default.
	other, _ := h.createOrg("TplOther", "Owner Other", "tplother@jjne.test", "0722000199", "supersecret")
	c.do(http.MethodPost, "/units/bulk-template", map[string]any{
		"unit_ids": []string{fix.unitIDs[0]}, "template_id": nil,
	}).mustStatus(t, http.StatusOK, "clear unit template")
	if id, src := resolvedTemplate(t, c, fix.unitIDs[0]); id != block || src != "property" {
		t.Errorf("cleared shop resolves %s/%s, want the property's", id, src)
	}
	c.do(http.MethodDelete, "/contract-templates/"+shop, nil).
		mustStatus(t, http.StatusNoContent, "delete once unassigned")

	// Another org naming this org's unit matches nothing.
	if got := mustFloat(t, other.do(http.MethodPost, "/units/bulk-template", map[string]any{
		"unit_ids": []string{fix.unitIDs[1]}, "template_id": nil,
	}).mustStatus(t, http.StatusOK, "foreign bulk").Body, "updated"); got != 0 {
		t.Errorf("foreign bulk updated = %v, want 0", got)
	}
	if id, _ := resolvedTemplate(t, c, fix.unitIDs[1]); id != block {
		t.Errorf("flat template changed by another org: %s", id)
	}

	// A template from another org is refused, not assigned.
	foreign := newTemplate(t, other, "Foreign")
	c.do(http.MethodPut, "/properties/"+fix.propertyID+"/template", map[string]any{"template_id": foreign}).
		mustStatus(t, http.StatusBadRequest, "foreign template")
}

func TestPhase22ApprovalWritesTheResolvedOrPickedTemplate(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("TplApprove", "tplapprove@jjne.test", "0722000200",
		[]string{"Shop 1", "Flat 1"}, 300_000)
	c := fix.client
	shop := newTemplate(t, c, "Commercial lease")
	picked := newTemplate(t, c, "Special terms")
	c.do(http.MethodPost, "/units/bulk-template", map[string]any{
		"unit_ids": []string{fix.unitIDs[0]}, "template_id": shop,
	}).mustStatus(t, http.StatusOK, "unit template")
	period := c.periodIDByDays(t, 30)

	apply := func(phone, unitCode string) string {
		renter := h.registerRenter(phone, "Renter "+phone, defaultPIN)
		renter.completeProfile(t, "Renter "+phone, validNIDA)
		return renter.do(http.MethodPost, "/units/"+unitCode+"/link", linkBody(period, testTermDays)).
			mustStatus(t, http.StatusCreated, "apply").str(t, "request", "id")
	}

	// No body: the unit's own template.
	first := c.do(http.MethodPost, "/link-requests/"+apply("+255722000201", fix.unitCodes[0])+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")
	if got := first.str(t, "contract", "template_id"); got != shop {
		t.Errorf("contract template = %s, want the unit's %s", got, shop)
	}

	// A template picked on the approve sheet wins over the unit's chain.
	second := c.do(http.MethodPost, "/link-requests/"+apply("+255722000202", fix.unitCodes[1])+"/approve",
		map[string]any{"template_id": picked}).mustStatus(t, http.StatusOK, "approve with pick")
	if got := second.str(t, "contract", "template_id"); got != picked {
		t.Errorf("contract template = %s, want the picked %s", got, picked)
	}
}
