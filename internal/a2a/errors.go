package a2a

import "github.com/tesserix/agentic-registry/pkg/api/v1alpha1"

// NotAnAgentError is returned by Card when asked to render a non-Agent kind.
// An A2A Agent Card is only defined for the Agent kind.
type NotAnAgentError struct {
	Kind v1alpha1.Kind
}

func (e *NotAnAgentError) Error() string {
	return "A2A agent card is only defined for the Agent kind, got " + string(e.Kind)
}
