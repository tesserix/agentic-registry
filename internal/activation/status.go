package activation

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type DesiredState string

const (
	Draft      DesiredState = "draft"
	Published  DesiredState = "published"
	Deprecated DesiredState = "deprecated"
	Retired    DesiredState = "retired"
)

type Phase string

const (
	DraftPhase      Phase = "draft"
	PublishedPhase  Phase = "published"
	Deployed        Phase = "deployed"
	Probed          Phase = "probed"
	Active          Phase = "active"
	Degraded        Phase = "degraded"
	DeprecatedPhase Phase = "deprecated"
	RetiredPhase    Phase = "retired"
	Failed          Phase = "failed"
)

type ConditionType string

const (
	PublishedCondition ConditionType = "Published"
	DeploymentReady    ConditionType = "DeploymentReady"
	ProbeReady         ConditionType = "ProbeReady"
	RouteReady         ConditionType = "RouteReady"
	Healthy            ConditionType = "Healthy"
	FailedCondition    ConditionType = "Failed"
)

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type Actor string

const (
	Registry          Actor = "registry"
	GatewayReconciler Actor = "gateway-reconciler"
	ProtocolProber    Actor = "protocol-prober"
)

type Condition struct {
	Type               ConditionType   `json:"type"`
	Status             ConditionStatus `json:"status"`
	Actor              Actor           `json:"actor"`
	Reason             string          `json:"reason"`
	ObservedGeneration int64           `json:"observedGeneration"`
	RegistryDigest     string          `json:"registryDigest"`
	ArtifactDigest     string          `json:"artifactDigest"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
	RequestID          string          `json:"requestId"`
}

type Observation struct {
	Type               ConditionType
	Status             ConditionStatus
	Actor              Actor
	Reason             string
	ObservedGeneration int64
	RegistryDigest     string
	ArtifactDigest     string
	RequestID          string
	ObservedAt         time.Time
}

type Status struct {
	SchemaVersion  string       `json:"schemaVersion"`
	Ref            string       `json:"ref"`
	RegistryDigest string       `json:"registryDigest"`
	ArtifactDigest string       `json:"artifactDigest"`
	Generation     int64        `json:"generation"`
	DesiredState   DesiredState `json:"desiredState"`
	Phase          Phase        `json:"phase"`
	PublishedAt    *time.Time   `json:"publishedAt"`
	ActiveAt       *time.Time   `json:"activeAt"`
	ObservedAt     time.Time    `json:"observedAt"`
	Conditions     []Condition  `json:"conditions"`
}

func New(ref, registryDigest, artifactDigest string, desired DesiredState, generation int64, now time.Time) Status {
	status := Status{
		SchemaVersion: "v1alpha1", Ref: ref, RegistryDigest: registryDigest, ArtifactDigest: artifactDigest,
		Generation: generation, DesiredState: desired, ObservedAt: now.UTC(),
	}
	if desired == Published || desired == Deprecated || desired == Retired {
		publishedAt := now.UTC()
		status.PublishedAt = &publishedAt
		status.Conditions = []Condition{{
			Type: PublishedCondition, Status: ConditionTrue, Actor: Registry,
			Reason: "Published", ObservedGeneration: generation,
			RegistryDigest: registryDigest, ArtifactDigest: artifactDigest,
			LastTransitionTime: publishedAt, RequestID: "registry-publication",
		}}
	}
	status.Phase = status.derivePhase()
	return status
}

func (s Status) Observe(observation Observation) (Status, error) {
	if err := s.validate(observation); err != nil {
		return Status{}, err
	}
	next := s
	next.Conditions = append([]Condition(nil), s.Conditions...)
	condition := Condition{
		Type: observation.Type, Status: observation.Status, Actor: observation.Actor,
		Reason: observation.Reason, ObservedGeneration: observation.ObservedGeneration,
		RegistryDigest: observation.RegistryDigest, ArtifactDigest: observation.ArtifactDigest,
		LastTransitionTime: observation.ObservedAt.UTC(), RequestID: observation.RequestID,
	}
	for index, existing := range next.Conditions {
		if existing.Type != condition.Type {
			continue
		}
		if existing.Status == condition.Status && existing.Reason == condition.Reason {
			condition.LastTransitionTime = existing.LastTransitionTime
		}
		next.Conditions[index] = condition
		next.ObservedAt = observation.ObservedAt.UTC()
		next.Phase = next.derivePhase()
		next.recordActivation(observation.ObservedAt)
		return next, nil
	}
	next.Conditions = append(next.Conditions, condition)
	sort.Slice(next.Conditions, func(i, j int) bool {
		return conditionRank(next.Conditions[i].Type) < conditionRank(next.Conditions[j].Type)
	})
	next.ObservedAt = observation.ObservedAt.UTC()
	next.Phase = next.derivePhase()
	next.recordActivation(observation.ObservedAt)
	return next, nil
}

func (s *Status) recordActivation(observedAt time.Time) {
	if s.Phase != Active || s.ActiveAt != nil {
		return
	}
	activatedAt := observedAt.UTC()
	s.ActiveAt = &activatedAt
}

func (s Status) validate(observation Observation) error {
	if s.Generation < 1 || observation.ObservedGeneration != s.Generation || observation.RegistryDigest != s.RegistryDigest || observation.ArtifactDigest != s.ArtifactDigest {
		return errors.New("activation observation does not match current identity")
	}
	if observation.ObservedAt.IsZero() || observation.Reason == "" || observation.RequestID == "" {
		return errors.New("activation observation is incomplete")
	}
	if observation.Status != ConditionTrue && observation.Status != ConditionFalse && observation.Status != ConditionUnknown {
		return fmt.Errorf("invalid activation condition status %q", observation.Status)
	}
	if ActorForCondition(observation.Type) != observation.Actor {
		return fmt.Errorf("actor %q does not own condition %q", observation.Actor, observation.Type)
	}
	return nil
}

// ActorForCondition returns the only actor permitted to report condition.
func ActorForCondition(condition ConditionType) Actor {
	switch condition {
	case PublishedCondition, FailedCondition:
		return Registry
	case DeploymentReady, RouteReady:
		return GatewayReconciler
	case ProbeReady, Healthy:
		return ProtocolProber
	default:
		return ""
	}
}

func (s Status) derivePhase() Phase {
	if s.DesiredState == Draft {
		return DraftPhase
	}
	if s.DesiredState == Deprecated {
		return DeprecatedPhase
	}
	if s.DesiredState == Retired {
		return RetiredPhase
	}
	if s.condition(FailedCondition) == ConditionTrue {
		return Failed
	}
	if s.condition(RouteReady) == ConditionTrue && s.condition(DeploymentReady) == ConditionTrue && s.condition(ProbeReady) == ConditionTrue && s.condition(Healthy) == ConditionTrue {
		return Active
	}
	if s.condition(DeploymentReady) == ConditionTrue && s.condition(ProbeReady) == ConditionTrue && s.condition(Healthy) == ConditionTrue {
		return Probed
	}
	if s.condition(DeploymentReady) == ConditionTrue {
		if s.ActiveAt != nil {
			return Degraded
		}
		return Deployed
	}
	return PublishedPhase
}

func (s Status) condition(conditionType ConditionType) ConditionStatus {
	for _, condition := range s.Conditions {
		if condition.Type == conditionType {
			return condition.Status
		}
	}
	return ConditionUnknown
}

func conditionRank(condition ConditionType) int {
	switch condition {
	case PublishedCondition:
		return 0
	case DeploymentReady:
		return 1
	case ProbeReady:
		return 2
	case RouteReady:
		return 3
	case Healthy:
		return 4
	case FailedCondition:
		return 5
	default:
		return 6
	}
}
