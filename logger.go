package tcpause

// Logger defines simple methods for info and debugging messages
// that could be of interest for the outside
type Logger interface {
	Debug(string)
	Info(string)
}

// nullLogger is a logger that does nothing
type nullLogger struct {
}

// NewNullLogger returns a new instance of a logger that does nothing
func NewNullLogger() Logger {
	return nullLogger{}
}

// Debug logs debug messages
func (l nullLogger) Debug(string) {
}

// Info logs info messages
func (l nullLogger) Info(string) {
}
