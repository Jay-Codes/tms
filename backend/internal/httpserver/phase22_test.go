package httpserver_test

import (
	"net/http"
	"strings"
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

// ------------------------------------------------ 22.2 contract policies --

func TestPhase22PolicyIsCopiedOntoTheContractAndHashed(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("TplPolicy", "tplpolicy@jjne.test", "0722000300", []string{"Flat 1"}, 300_000)
	c := fix.client
	policy := map[string]any{
		"move_out_proration": "pro_rata", "early_exit_prepaid": "refund",
		"deposit_mode": "months", "deposit_months": 2, "deductions_may_exceed_deposit": false,
		"tenant_notice_days": 30, "eviction_notice_days": 14,
	}

	// Rules are checked, field by field.
	bad := c.do(http.MethodPost, "/contract-templates", map[string]any{
		"name": "Bad", "body_html": "<p>x</p>",
		"policy": map[string]any{"move_out_proration": "weekly", "early_exit_prepaid": "refund",
			"deposit_mode": "none"},
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad policy = %d, want 400 — %s", bad.Code, bad.Raw)
	}

	tpl := c.do(http.MethodPost, "/contract-templates", map[string]any{
		"name": "With deposit", "policy": policy,
		"body_html": "<p>Deposit {{deposit}}. Notice {{tenant_notice_days}} days.</p>",
	}).mustStatus(t, http.StatusCreated, "template with policy")
	tplID := tpl.str(t, "template", "id")
	if got := tpl.str(t, "template", "policy", "deposit_mode"); got != "months" {
		t.Errorf("template policy deposit_mode = %q", got)
	}
	c.do(http.MethodPost, "/units/bulk-template", map[string]any{
		"unit_ids": []string{fix.unitIDs[0]}, "template_id": tplID,
	}).mustStatus(t, http.StatusOK, "assign")

	renter := h.registerRenter("+255722000301", "Policy Renter", defaultPIN)
	renter.completeProfile(t, "Policy Renter", validNIDA)
	reqID := renter.do(http.MethodPost, "/units/"+fix.unitCodes[0]+"/link",
		linkBody(c.periodIDByDays(t, 30), testTermDays)).
		mustStatus(t, http.StatusCreated, "apply").str(t, "request", "id")
	approved := c.do(http.MethodPost, "/link-requests/"+reqID+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")
	contractID := approved.str(t, "contract", "id")

	// The deposit in months is resolved to an amount on this tenancy.
	if got := policyNum(t, approved.Body, "deposit_amount"); got != 600_000 {
		t.Errorf("contract deposit_amount = %v, want 600000", got)
	}
	doc := c.do(http.MethodGet, "/contracts/"+contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document")
	if !strings.Contains(doc.Raw, "600,000") || !strings.Contains(doc.Raw, "Notice 30 days") {
		t.Errorf("document does not state the policy: %s", doc.Raw)
	}

	// Changing the template afterwards changes nothing already written, and
	// the hash still verifies.
	c.do(http.MethodPatch, "/contract-templates/"+tplID, map[string]any{"policy": nil}).
		mustStatus(t, http.StatusOK, "clear policy")
	v := c.do(http.MethodGet, "/contracts/"+contractID+"/verify", nil).mustStatus(t, http.StatusOK, "verify")
	if valid, _ := v.Body["valid"].(bool); !valid {
		t.Errorf("verify = %s, want valid", v.Raw)
	}
	again := c.do(http.MethodGet, "/contracts/"+contractID, nil).mustStatus(t, http.StatusOK, "contract")
	if got := policyNum(t, again.Body, "tenant_notice_days"); got != 30 {
		t.Errorf("contract notice after template change = %v, want 30", got)
	}
}

// policyNum reads body.contract.policy[key].
func policyNum(t *testing.T, body map[string]any, key string) float64 {
	t.Helper()
	c, _ := body["contract"].(map[string]any)
	p, _ := c["policy"].(map[string]any)
	return mustFloat(t, p, key)
}
