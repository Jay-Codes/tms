package audit

// Phase 28 — projections (PLAN2 Phase 28). A saved scenario is a named set of
// "what if" parameters; creating and deleting one is recorded so the audit
// page can say who left which assumption lying around. The investment fields
// on a property ride on `property.update`, and a category's capital flag on
// `expense_category.update`, like every other edit to those rows.
const (
	ActionProjectionScenarioCreate = "projection_scenario.create"
	ActionProjectionScenarioDelete = "projection_scenario.delete"

	EntityProjectionScenario = "projection_scenario"
)
