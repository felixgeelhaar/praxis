package domain

type ActionError struct {
	Code      string
	Message   string
	Vendor    map[string]any
	Retryable bool
}
