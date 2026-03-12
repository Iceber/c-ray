package models

// Pod represents a Kubernetes pod (extracted from container labels)
type Pod struct {
	Name      string
	Namespace string
	UID       string

	// Containers in this pod
	Containers []*Container
}
