package agent

type Provider string

const (
	Claude Provider = "claude"
	Codex  Provider = "codex"
)

// spec is what AgentDiff knows about the CLI of one provider.
type spec struct {
	id   Provider
	name string
	// efforts are the effort levels the CLI takes.
	efforts []string
	args    func(Config, Request) []string
	// stdin is the text the CLI reads on its standard input.
	stdin  func(Request) string
	parser func(dir string) lineParser
	// resumed reads the resumed session from the words of a logged
	// command.
	resumed  func(words []string) string
	logItems func(raw []byte) []LogItem
}

// specs are the providers, in the order the settings show them.
var specs = []spec{
	{
		id:       Claude,
		name:     "Claude Code",
		efforts:  []string{"low", "medium", "high", "xhigh", "max"},
		args:     claudeArgs,
		stdin:    claudePrompt,
		parser:   func(dir string) lineParser { return &claudeParser{dir: dir} },
		resumed:  claudeResumed,
		logItems: claudeItems,
	},
	{
		id:       Codex,
		name:     "Codex",
		efforts:  []string{"minimal", "low", "medium", "high", "xhigh"},
		args:     codexArgs,
		stdin:    codexPrompt,
		parser:   func(string) lineParser { return &codexParser{} },
		resumed:  codexResumed,
		logItems: codexItems,
	},
}

func lookup(p Provider) (spec, bool) {
	for _, sp := range specs {
		if sp.id == p {
			return sp, true
		}
	}
	return spec{}, false
}

// specOf is the spec of p. An unknown provider, as the empty one of an old
// review, reads as Claude, the default provider.
func specOf(p Provider) spec {
	if sp, ok := lookup(p); ok {
		return sp
	}
	return specs[0]
}

// Providers lists the providers, in the order the settings show them.
func Providers() []Provider {
	ids := make([]Provider, len(specs))
	for i, sp := range specs {
		ids[i] = sp.id
	}
	return ids
}

// Name is the name of the provider's CLI, as the app shows it.
func (p Provider) Name() string { return specOf(p).name }

// Efforts lists the effort levels the CLI of the provider takes.
func (p Provider) Efforts() []string { return specOf(p).efforts }
