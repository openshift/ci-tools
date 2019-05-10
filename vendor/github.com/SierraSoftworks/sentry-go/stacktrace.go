package sentry

var defaultInternalPrefixes = []string{"main"}

// AddInternalPrefixes allows you to easily add packages which will be considered
// "internal" in your stack traces.
func AddInternalPrefixes(prefixes ...string) {
	defaultInternalPrefixes = append(defaultInternalPrefixes, prefixes...)
}

// StackTraceOption wraps a stacktrace and gives you tools for selecting
// where it is sourced from or what is classified as an internal module.
type StackTraceOption interface {
	Option
	ForError(err error) StackTraceOption
	WithInternalPrefixes(prefixes ...string) StackTraceOption
}

// StackTrace allows you to add a StackTrace to the event you submit to Sentry,
// allowing you to quickly determine where in your code the event was generated.
func StackTrace() StackTraceOption {
	return &stackTraceOption{
		Frames:  getStacktraceFrames(0),
		Omitted: []int{},

		internalPrefixes: defaultInternalPrefixes,
	}
}

type stackTraceOption struct {
	Frames  stackTraceFrames `json:"frames"`
	Omitted []int            `json:"frames_omitted,omitempty"`

	internalPrefixes []string
}

func (o *stackTraceOption) Class() string {
	return "stacktrace"
}

func (o *stackTraceOption) ForError(err error) StackTraceOption {
	newFrames := getStacktraceFramesForError(err)
	if newFrames.Len() > 0 {
		o.Frames = newFrames
	}

	return o
}

func (o *stackTraceOption) WithInternalPrefixes(prefixes ...string) StackTraceOption {
	o.internalPrefixes = append(o.internalPrefixes, prefixes...)
	return o
}

func (o *stackTraceOption) Finalize() {
	for _, frame := range o.Frames {
		frame.ClassifyInternal(o.internalPrefixes)
	}
}

// stackTraceFrame describes the StackTrace for a given
// exception or thread.
type stackTraceFrame struct {
	// At least one of the following must be present
	Filename string `json:"filename,omitempty"`
	Function string `json:"function,omitempty"`
	Module   string `json:"module,omitempty"`

	// These fields are optional
	Line              int                    `json:"lineno,omitempty"`
	Column            int                    `json:"colno,omitempty"`
	AbsoluteFilename  string                 `json:"abs_path,omitempty"`
	PreContext        []string               `json:"pre_context,omitempty"`
	Context           string                 `json:"context_line,omitempty"`
	PostContext       []string               `json:"post_context,omitempty"`
	InApp             bool                   `json:"in_app"`
	Variables         map[string]interface{} `json:"vars,omitempty"`
	Package           string                 `json:"package,omitempty"`
	Platform          string                 `json:"platform,omitempty"`
	ImageAddress      string                 `json:"image_addr,omitempty"`
	SymbolAddress     string                 `json:"symbol_addr,omitempty"`
	InstructionOffset int                    `json:"instruction_offset,omitempty"`
}
