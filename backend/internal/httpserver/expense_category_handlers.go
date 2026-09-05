package httpserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/expense"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

func notFoundCategory(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such expense category")
}

// ensureExpenseCategories seeds an org that has none, and reports whether it
// did. The count check runs outside the transaction on purpose: the common case
// is an org that already has its categories, and that case must cost one cheap
// count rather than a transaction per read.
func (s *Server) ensureExpenseCategories(ctx context.Context, orgID pgtype.UUID) error {
	n, err := s.q.CountExpenseCategories(ctx, orgID)
	if err != nil || n > 0 {
		return err
	}
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		return expense.SeedCategories(ctx, q, orgID)
	})
}

// ------------------------------------------- GET /org/expense-categories --

// handleListExpenseCategories returns every live category, active or not: the
// settings screen has to show a deactivated one in order to reactivate it, and
// a ledger row filed under one has to be able to name it.
func (s *Server) handleListExpenseCategories(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	if err := s.ensureExpenseCategories(r.Context(), p.OrgID); err != nil {
		s.serverError(w, r, "expense_categories.seed", err)
		return
	}
	rows, err := s.q.ListExpenseCategories(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "expense_categories.list", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": toCategories(rows)})
}

// ------------------------------------------ POST /org/expense-categories --

func (s *Server) handleCreateExpenseCategory(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Name      string `json:"name"`
		SortOrder *int32 `json:"sort_order"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), categoryNameMax)
	sortOrder := int32(0)
	if body.SortOrder != nil {
		sortOrder = checkSortOrder(f, "sort_order", *body.SortOrder)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// A category appended without a position goes to the end of the list, the
	// way a new payment period does.
	if body.SortOrder == nil {
		maxOrder, err := s.q.MaxExpenseCategorySortOrder(r.Context(), p.OrgID)
		if err != nil {
			s.serverError(w, r, "expense_categories.create.sort", err)
			return
		}
		sortOrder = maxOrder + 1
	}

	var created sqlc.ExpenseCategory
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateExpenseCategory(r.Context(), sqlc.CreateExpenseCategoryParams{
			OrgID: p.OrgID, Name: name, IsDefault: false, SortOrder: sortOrder,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseCategoryCreate,
			EntityType:  audit.EntityExpenseCategory,
			EntityID:    db.UUIDString(created.ID),
			After:       toCategory(created),
		})
	})
	if isUnique(err) {
		conflictCode(w, "category_exists", "duplicate category",
			"this organisation already has a category with that name")
		return
	}
	if err != nil {
		s.serverError(w, r, "expense_categories.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"category": toCategory(created)})
}

// ------------------------------------ PATCH /org/expense-categories/{id} --

func (s *Server) handlePatchExpenseCategory(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundCategory(w)
		return
	}

	var body struct {
		Name      *string `json:"name"`
		SortOrder *int32  `json:"sort_order"`
		Active    *bool   `json:"active"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	params := sqlc.UpdateExpenseCategoryParams{OrgID: p.OrgID, ID: id, Active: body.Active}
	if body.Name != nil {
		name := f.MaxLen("name", f.Required("name", *body.Name), categoryNameMax)
		params.Name = &name
	}
	if body.SortOrder != nil {
		order := checkSortOrder(f, "sort_order", *body.SortOrder)
		params.SortOrder = &order
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	before, err := s.q.GetExpenseCategory(r.Context(), sqlc.GetExpenseCategoryParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundCategory(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense_categories.patch.get", err)
		return
	}

	var updated sqlc.ExpenseCategory
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateExpenseCategory(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseCategoryUpdate,
			EntityType:  audit.EntityExpenseCategory,
			EntityID:    db.UUIDString(id),
			Before:      toCategory(before),
			After:       toCategory(updated),
		})
	})
	if isUnique(err) {
		conflictCode(w, "category_exists", "duplicate category",
			"this organisation already has a category with that name")
		return
	}
	if isNoRows(err) {
		notFoundCategory(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense_categories.patch.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"category": toCategory(updated)})
}

// ----------------------------------- DELETE /org/expense-categories/{id} --

// handleDeleteExpenseCategory soft-deletes an unused category. One an expense
// is filed under cannot go: the ledger would lose the word the landlord filed
// it under, so the answer is 409 and the fix is to deactivate it instead —
// which keeps history readable while taking it off the picker (FLOWS 12.5).
func (s *Server) handleDeleteExpenseCategory(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundCategory(w)
		return
	}

	before, err := s.q.GetExpenseCategory(r.Context(), sqlc.GetExpenseCategoryParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundCategory(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense_categories.delete.get", err)
		return
	}
	inUse, err := s.q.CountExpensesInCategory(r.Context(), sqlc.CountExpensesInCategoryParams{
		OrgID: p.OrgID, CategoryID: id,
	})
	if err != nil {
		s.serverError(w, r, "expense_categories.delete.count", err)
		return
	}
	if inUse > 0 {
		conflictCode(w, "category_in_use", "category is in use",
			"expenses are filed under this category; deactivate it instead of deleting it")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SoftDeleteExpenseCategory(r.Context(), sqlc.SoftDeleteExpenseCategoryParams{
			OrgID: p.OrgID, ID: id,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseCategoryDelete,
			EntityType:  audit.EntityExpenseCategory,
			EntityID:    db.UUIDString(id),
			Before:      toCategory(before),
		})
	}); err != nil {
		if isNoRows(err) {
			notFoundCategory(w)
			return
		}
		s.serverError(w, r, "expense_categories.delete.tx", err)
		return
	}
	NoContent(w)
}

// --------------------------------------------------------------- helpers --

// checkSortOrder bounds a category's position. The number only orders a list of
// at most a few dozen rows, so anything outside a small range is a typo.
func checkSortOrder(f validate.Fields, field string, order int32) int32 {
	if order < 0 || order > categorySortMax {
		f.Add(field, "must be between 0 and "+strconv.Itoa(categorySortMax))
	}
	return order
}
