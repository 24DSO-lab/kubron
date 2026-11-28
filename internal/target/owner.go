// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

import "strings"

// OwnerKind — вид владельца группы.
type OwnerKind string

const (
	OwnerOrganization OwnerKind = "organization"
	OwnerTeam         OwnerKind = "team"
)

// OwnerScope — владелец Target Group: ровно Organization(ID) либо Team(ID). Пустые ID, неизвестный вид и
// implicit/default owner недопустимы. Team всегда несёт и свою Organization: так принадлежность команды к
// Organization проверяется явно, а не предполагается по уникальности TeamID (как в identity.OwnerScope).
//
// Organization: OrgID обязателен, TeamID пуст. Team: OrgID и TeamID обязательны.
// В локальном Desktop используется Organization с зарезервированным ID LocalOrgID (ADR-0053); Team там не
// поддерживается. Не заменяет tenant.Scope (тот требует ClusterID) и не выводится из создателя.
type OwnerScope struct {
	Kind   OwnerKind
	OrgID  string
	TeamID string
}

// LocalOrgID — зарезервированный ID локальной single-user Organization (File/Desktop, ADR-0053).
const LocalOrgID = "local"

// OrganizationOwner создаёт Organization owner scope.
func OrganizationOwner(orgID string) OwnerScope {
	return OwnerScope{Kind: OwnerOrganization, OrgID: orgID}
}

// TeamOwner создаёт Team owner scope.
func TeamOwner(orgID, teamID string) OwnerScope {
	return OwnerScope{Kind: OwnerTeam, OrgID: orgID, TeamID: teamID}
}

// Validate проверяет допустимую комбинацию без fallback'ов.
func (o OwnerScope) Validate() error {
	if !validID(o.OrgID) {
		return invalid("OwnerScope.OrgID")
	}
	switch o.Kind {
	case OwnerOrganization:
		if o.TeamID != "" {
			return invalid("OwnerScope.TeamID")
		}
	case OwnerTeam:
		if !validID(o.TeamID) {
			return invalid("OwnerScope.TeamID")
		}
		if o.OrgID == LocalOrgID {
			return invalid("OwnerScope.Kind") // в локальной области Team не существует
		}
	default:
		return invalid("OwnerScope.Kind")
	}
	return nil
}

func compareOwners(a, b OwnerScope) int {
	if c := strings.Compare(string(a.Kind), string(b.Kind)); c != 0 {
		return c
	}
	if c := strings.Compare(a.OrgID, b.OrgID); c != 0 {
		return c
	}
	return strings.Compare(a.TeamID, b.TeamID)
}

// ActorKind — вид инициатора действия.
type ActorKind string

const (
	ActorUser      ActorKind = "user"
	ActorSystem    ActorKind = "system"
	ActorScheduler ActorKind = "scheduler"
)

// Actor — доверенный (определённый сервером) инициатор. Для мутаций групп допустим только ActorUser.
// Creator хранится как provenance: он не владелец, не authority и не grant.
type Actor struct {
	Kind ActorKind
	ID   string
}

// Validate проверяет вид и ID.
func (a Actor) Validate() error {
	switch a.Kind {
	case ActorUser, ActorSystem, ActorScheduler:
	default:
		return invalid("Actor.Kind")
	}
	if !validID(a.ID) {
		return invalid("Actor.ID")
	}
	return nil
}

// Viewer — доверенное множество owner scope'ов, группы которых видны actor'у (членство в Organization/Team
// и принятые sharing-получатели). Определяется сервером (Access.ViewerFor), клиентом не задаётся. Видимость не
// даёт права изменять группу, grants, delegation или Kubernetes access.
type Viewer struct {
	Scopes []OwnerScope
}

// Validate проверяет все scope'ы.
func (v Viewer) Validate() error {
	for _, s := range v.Scopes {
		if err := s.Validate(); err != nil {
			return invalid("Viewer.Scopes")
		}
	}
	return nil
}

// CanSeeOwner сообщает, что scope входит в множество viewer'а (точное совпадение: Organization не включает её Teams).
func (v Viewer) CanSeeOwner(o OwnerScope) bool {
	for _, s := range v.Scopes {
		if s == o {
			return true
		}
	}
	return false
}

// CanSee сообщает, что группа видна: viewer владеет ей либо она расшарена его scope'у (SharedWith).
func (v Viewer) CanSee(g Group) bool {
	if v.CanSeeOwner(g.Owner) {
		return true
	}
	for _, s := range g.Sharing.SharedWith {
		if v.CanSeeOwner(s) {
			return true
		}
	}
	return false
}

// ClusterOwner — доверенные факты владения кластером из существующего cluster catalog (ADR-0053). Пустой
// TeamID означает Organization-owned кластер; он НЕ wildcard и не повышается до Organization scope.
type ClusterOwner struct {
	OrgID  string
	TeamID string
}

// Scope возвращает канонический owner scope кластера либо ErrInvalid для неполного/некорректного результата.
func (c ClusterOwner) Scope() (OwnerScope, error) {
	var s OwnerScope
	if c.TeamID == "" {
		s = OrganizationOwner(c.OrgID)
	} else {
		s = TeamOwner(c.OrgID, c.TeamID)
	}
	if err := s.Validate(); err != nil {
		return OwnerScope{}, err
	}
	return s, nil
}

// OwnerCompatible сообщает, что кластер можно включать в группу этого владельца: owner scope кластера обязан
// ТОЧНО совпадать с owner scope группы (Organization-группа — Organization-кластеры своей Organization;
// Team-группа — кластеры своей Team). Organization ownership не даёт доступа к Team-owned target'у, а чужая
// Organization несовместима всегда. Cross-Team включение требует отдельной delegation (ADR-0037/0041) и
// проверяется будущими Plan/side-effect слоями; в Phase 2 оно fail closed.
func OwnerCompatible(group OwnerScope, cluster ClusterOwner) bool {
	cs, err := cluster.Scope()
	if err != nil || group.Validate() != nil {
		return false
	}
	return cs == group
}
