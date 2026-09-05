// Package audit records an immutable trail of every mutating action
// (SPEC §8). Audit rows are insert-only: no UPDATE or DELETE grants.
package audit
