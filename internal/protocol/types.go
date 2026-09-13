package protocol

type Position struct {
	Line      uint `json:"line"`
	Character uint `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LSP DiagnosticSeverity values.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// LSP DiagnosticTag values.
const (
	DiagnosticTagUnnecessary = 1
	DiagnosticTagDeprecated  = 2
)

// DiagnosticRelatedInformation points from a diagnostic to another location
// that is relevant to it (e.g. the duplicate definition site, or the place
// a symbol is also referenced). LSP spec: DiagnosticRelatedInformation.
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           int                            `json:"severity"`
	Source             string                         `json:"source"`
	Message            string                         `json:"message"`
	Code               string                         `json:"code,omitempty"`
	Tags               []int                          `json:"tags,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}

func LineRange(line, start, end uint) Range {
	return Range{
		Start: Position{Line: line, Character: start},
		End:   Position{Line: line, Character: end},
	}
}
