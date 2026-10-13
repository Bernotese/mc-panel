package monitoring

import "context"

// alerter evaluates the alert rules after every pass (Phase A stub; B2 implements it).
type alerter struct{ d Deps }

func newAlerter(d Deps) *alerter { return &alerter{d: d} }

var _ = (*alerter).deps // Phase A stub: d is used once B2 implements the alerter

func (a *alerter) deps() Deps { return a.d }

// Evaluate checks the rules against a pass and notifies on transitions.
func (a *alerter) Evaluate(context.Context, Snapshot) {}

// Firing lists the alerts firing right now.
func (a *alerter) Firing(context.Context) []FiringAlert { return nil }
