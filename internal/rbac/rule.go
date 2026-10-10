// Package rbac declares and enforces access rules for every API route.
package rbac

import (
	"fmt"
	"slices"

	"github.com/Bernotese/mc-panel/internal/principal"
)

type Permission string

const (
	PermServerView    Permission = "server.view"
	PermServerPower   Permission = "server.power"
	PermServerConsole Permission = "server.console"
	PermServerFiles   Permission = "server.files"
	PermServerPlugins Permission = "server.plugins"
	PermServerBackups Permission = "server.backups"
)

var AllPermissions = []Permission{
	PermServerView, PermServerPower, PermServerConsole, PermServerFiles, PermServerPlugins, PermServerBackups,
}

func (p Permission) Valid() bool { return slices.Contains(AllPermissions, p) }

type ruleKind int

const (
	kindInvalid ruleKind = iota
	kindPublic
	kindAuthenticated
	kindAdmin
	kindServer
)

// Rule describes who may call a route. The zero value is invalid on purpose.
type Rule struct {
	kind   ruleKind
	perm   Permission
	param  string
	sudo   bool
	stages []principal.Stage
}

func full() []principal.Stage { return []principal.Stage{principal.StageFull} }

func Public() Rule        { return Rule{kind: kindPublic} }
func Authenticated() Rule { return Rule{kind: kindAuthenticated, stages: full()} }
func Admin() Rule         { return Rule{kind: kindAdmin, stages: full()} }

func Server(p Permission, param string) Rule {
	if !p.Valid() || param == "" {
		panic(fmt.Sprintf("rbac: invalid server rule %q/%q", p, param))
	}
	return Rule{kind: kindServer, perm: p, param: param, stages: full()}
}

func (r Rule) WithSudo() Rule {
	if r.kind == kindPublic {
		panic("rbac: public routes cannot require sudo")
	}
	r.sudo = true
	return r
}

func (r Rule) AllowStages(s ...principal.Stage) Rule {
	if r.kind == kindPublic {
		panic("rbac: public routes have no stages")
	}
	if len(s) == 0 {
		panic("rbac: AllowStages requires at least one stage")
	}
	r.stages = slices.Clone(s)
	return r
}

func (r Rule) IsPublic() bool     { return r.kind == kindPublic }
func (r Rule) RequiresSudo() bool { return r.sudo }

func (r Rule) String() string {
	var s string
	switch r.kind {
	case kindPublic:
		return "public"
	case kindAuthenticated:
		s = "authenticated"
	case kindAdmin:
		s = "admin"
	case kindServer:
		s = fmt.Sprintf("server(%s,{%s})", r.perm, r.param)
	default:
		return "invalid"
	}
	if r.sudo {
		s += "+sudo"
	}
	return fmt.Sprintf("%s%v", s, r.stages)
}
