package temporalrest

// Metadata holds public details about Temporal that shall be used in the "metadata"
// object of a REST response.
type Metadata struct {
	Workflow *MetadataWorkflow `json:"workflow,omitempty"`
}

// MetadataWorkflow holds public details about a Temporal workflow that shall be used
// in the "metadata" object of a REST response.
//
// ID is the workflow ID as the application chose it, and reaches the REST client
// verbatim: an ID built from personal data, a secret or an internal identifier
// discloses it to whoever reads the response.
type MetadataWorkflow struct {
	ID  string       `json:"id"`
	Run *MetadataRun `json:"run,omitempty"`
}

// MetadataRun holds public details about a Temporal run that shall be used in the
// "metadata" object of a REST response.
type MetadataRun struct {
	ID string `json:"id"`
}
