// Package script parses mIRC script files into their top-level definitions
// and the statement trees of their command bodies.
//
// Command text is kept unevaluated: mIRC evaluates identifiers and variables
// when a line runs, so the parser only decides structure (blocks, if/while,
// labels, command separators).
package script

// File is one loaded script file.
type File struct {
	Path    string
	Kind    Kind
	Aliases []*Alias
	Events  []*Event
	Menus   []*Menu
	Dialogs []*Dialog
	Groups  map[string]*Group
	// GroupOrder lists groups in the order they appear.
	GroupOrder []*Group
}

// Group is a #name on|off ... #name end section. Definitions inside a
// disabled group do not trigger; /enable and /disable flip On.
type Group struct {
	Name string
	On   bool
}

// Alias is an alias definition. Local aliases (-l) are visible only to
// commands in the same file.
type Alias struct {
	Name  string
	Local bool
	Body  Block
	Group *Group
	File  *File
	Line  int
}

// Event is an on, raw or ctcp definition.
type Event struct {
	// Kind is "on", "raw" or "ctcp".
	Kind string
	// Prefixes are the leading modifiers of the level field, such as ! & ^ * + @ $ and "me".
	Prefixes string
	// Me is set by the me: prefix.
	Me bool
	// Level is the access level, such as "1", "*" or "op".
	Level string
	// Name is the event name in upper case (TEXT, JOIN, ...), the raw numeric, or "CTCP".
	Name string
	// Fields are the event-specific fields between the name and the commands.
	Fields []string
	Body   Block
	Group  *Group
	File   *File
	Line   int
}

// Menu is a menu definition. Only menus for @windows are used by the runtime.
type Menu struct {
	Targets []string
	Lines   []string
	Group   *Group
	File    *File
	Line    int
}

// Dialog is a dialog table definition, kept as raw lines for the dialog engine.
type Dialog struct {
	Name  string
	Local bool
	Lines []string
	File  *File
	Line  int
}

// Block is a sequence of statements.
type Block []Stmt

// Stmt is one statement in a command body.
type Stmt interface{ stmt() }

// Cmd is a single command line, unevaluated.
type Cmd struct {
	Text string
	Line int
}

// If is an if/elseif/else chain.
type If struct {
	Branches []Branch
	Else     Block
	HasElse  bool
}

// Branch is one condition and its body.
type Branch struct {
	Cond string
	Body Block
	Line int
}

// While is a while loop.
type While struct {
	Cond string
	Body Block
	Line int
}

// Label is a :name goto target.
type Label struct {
	Name string
	Line int
}

// Nested is a bare { } block.
type Nested struct {
	Body Block
}

func (*Cmd) stmt()    {}
func (*If) stmt()     {}
func (*While) stmt()  {}
func (*Label) stmt()  {}
func (*Nested) stmt() {}
