package errorstack

/*
Option allows to set optional values when creating a new error with New or Wrap.
*/
type Option func(*Error)

/*
WithIntegration sets the integration at the origin of the error.
*/
func WithIntegration(name string) Option {
	return func(err *Error) {
		err.Integration = name
	}
}
