package coverage

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

func parseReport(in ReportInput) ([]MeasuredFile, error) {
	b := &reportBuilder{input: in, files: map[string]*fileBuilder{}}
	var err error
	switch in.Format {
	case "lcov":
		err = b.lcov()
	case "go":
		err = b.goCover()
	case "istanbul":
		err = b.istanbul()
	case "simplecov":
		err = b.simplecov()
	case "cobertura", "jacoco", "clover", "opencover", "sonar":
		err = b.xmlReport()
	default:
		err = fmt.Errorf("unsupported format; supported: %s", strings.Join(Formats, ", "))
	}
	if err != nil {
		return nil, err
	}
	return b.result()
}

func (b *reportBuilder) number(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		b.err = fmt.Errorf("invalid non-negative coverage count %q", s)
		return 0
	}
	return n
}

func (b *reportBuilder) lcov() error {
	s := bufio.NewScanner(strings.NewReader(b.input.Content))
	s.Buffer(make([]byte, 4096), 1<<20)
	var f *fileBuilder
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		key, value, _ := strings.Cut(line, ":")
		switch key {
		case "SF":
			if f != nil {
				return fmt.Errorf("missing end_of_record")
			}
			f = b.file(value)
		case "DA":
			v := strings.Split(value, ",")
			if f == nil || len(v) < 2 {
				return fmt.Errorf("invalid DA record")
			}
			b.line(f, b.number(v[0]), b.number(v[1]))
		case "BRDA":
			v := strings.Split(value, ",")
			if f == nil || len(v) != 4 {
				return fmt.Errorf("invalid BRDA record")
			}
			if b.number(v[0]) < 1 {
				return fmt.Errorf("invalid branch line")
			}
			hits := 0
			if v[3] != "-" && b.number(v[3]) > 0 {
				hits = 1
			}
			b.point(f, "branches", strings.Join(v[:3], ","), hits, 1)
		case "BRF":
			if f == nil {
				return fmt.Errorf("BRF outside a file")
			}
			if b.number(value) == 0 {
				b.point(f, "branches", "empty", 0, 0)
			}
		case "FNDA":
			count, name, ok := strings.Cut(value, ",")
			if f == nil || !ok || name == "" {
				return fmt.Errorf("invalid FNDA record")
			}
			covered := 0
			if b.number(count) > 0 {
				covered = 1
			}
			b.point(f, "functions", name, covered, 1)
		case "FNF":
			if f == nil {
				return fmt.Errorf("FNF outside a file")
			}
			if b.number(value) == 0 {
				b.point(f, "functions", "empty", 0, 0)
			}
		case "end_of_record":
			f = nil
		}
	}
	if f != nil {
		return fmt.Errorf("missing end_of_record")
	}
	return s.Err()
}

var goBlock = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+)\s+(\d+)\s+(\d+)$`)

func (b *reportBuilder) goCover() error {
	s := bufio.NewScanner(strings.NewReader(b.input.Content))
	s.Buffer(make([]byte, 4096), 1<<20)
	if !s.Scan() {
		return fmt.Errorf("missing Go coverage mode")
	}
	mode := strings.TrimSpace(s.Text())
	if mode != "mode: set" && mode != "mode: count" && mode != "mode: atomic" {
		return fmt.Errorf("invalid Go coverage mode")
	}
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "" {
			continue
		}
		m := goBlock.FindStringSubmatch(s.Text())
		if m == nil {
			return fmt.Errorf("invalid Go coverage block")
		}
		start, end := b.number(m[2]), b.number(m[4])
		if start < 1 || end < start || b.number(m[3]) < 1 || b.number(m[5]) < 1 {
			return fmt.Errorf("invalid Go coverage range")
		}
		total, covered := b.number(m[6]), 0
		if b.number(m[7]) > 0 {
			covered = total
		}
		// Go measures statements in basic blocks, not executable lines.
		b.point(b.file(m[1]), "statements", strings.Join(m[2:6], ":"), covered, total)
	}
	return s.Err()
}

type xmlElement struct {
	name     string
	attrs    map[string]string
	children []*xmlElement
}

func readCoverageXML(content string) (*xmlElement, error) {
	d := xml.NewDecoder(strings.NewReader(content))
	var root *xmlElement
	var stack []*xmlElement
	count := 0
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			count++
			if len(stack) >= 64 || count > 1000000 {
				return nil, fmt.Errorf("coverage XML nesting or element limit exceeded")
			}
			n := &xmlElement{name: v.Name.Local, attrs: map[string]string{}}
			for _, a := range v.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root != nil {
				return nil, fmt.Errorf("multiple coverage XML roots")
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				return nil, fmt.Errorf("unexpected text outside coverage XML")
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("empty coverage XML")
	}
	return root, nil
}

func (n *xmlElement) each(name string, fn func(*xmlElement)) {
	if n.name == name {
		fn(n)
		return
	}
	for _, c := range n.children {
		c.each(name, fn)
	}
}

func (n *xmlElement) child(name string) []*xmlElement {
	var out []*xmlElement
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

var conditionCount = regexp.MustCompile(`\((\d+)/(\d+)\)`)

func (b *reportBuilder) xmlReport() error {
	root, err := readCoverageXML(b.input.Content)
	if err != nil {
		return err
	}
	expected := map[string]string{"cobertura": "coverage", "clover": "coverage", "sonar": "coverage", "jacoco": "report", "opencover": "CoverageSession"}[b.input.Format]
	if root.name != expected {
		return fmt.Errorf("expected %s XML root", expected)
	}
	switch b.input.Format {
	case "cobertura":
		root.each("class", func(c *xmlElement) {
			f := b.file(c.attrs["filename"])
			for _, lines := range c.child("lines") {
				for _, l := range lines.child("line") {
					n := b.number(l.attrs["number"])
					b.line(f, n, b.number(l.attrs["hits"]))
					if l.attrs["branch"] == "true" {
						m := conditionCount.FindStringSubmatch(l.attrs["condition-coverage"])
						if m == nil {
							b.err = fmt.Errorf("branch line requires condition-coverage counts")
							continue
						}
						b.point(f, "branches", fmt.Sprint(n), b.number(m[1]), b.number(m[2]))
					} else {
						b.point(f, "branches", fmt.Sprint(n), 0, 0)
					}
				}
			}
		})
	case "jacoco":
		root.each("package", func(p *xmlElement) {
			for _, src := range p.child("sourcefile") {
				name := src.attrs["name"]
				if p.attrs["name"] != "" {
					name = p.attrs["name"] + "/" + name
				}
				f := b.file(name)
				for _, l := range src.child("line") {
					n := b.number(l.attrs["nr"])
					b.line(f, n, b.number(l.attrs["ci"]))
					covered, missed := 0, 0
					if l.attrs["cb"] != "" {
						covered = b.number(l.attrs["cb"])
					}
					if l.attrs["mb"] != "" {
						missed = b.number(l.attrs["mb"])
					}
					b.point(f, "branches", fmt.Sprint(n), covered, covered+missed)
				}
				for _, counter := range src.child("counter") {
					if counter.attrs["type"] == "METHOD" {
						covered, missed := b.number(counter.attrs["covered"]), b.number(counter.attrs["missed"])
						b.point(f, "functions", "methods", covered, covered+missed)
					}
				}
			}
		})
	case "sonar":
		if root.attrs["version"] != "1" {
			return fmt.Errorf("unsupported generic coverage version")
		}
		for _, src := range root.child("file") {
			f := b.file(src.attrs["path"])
			for _, l := range src.child("lineToCover") {
				n, hits := b.number(l.attrs["lineNumber"]), 0
				switch l.attrs["covered"] {
				case "true", "1":
					hits = 1
				case "false", "0":
				default:
					return fmt.Errorf("invalid covered boolean")
				}
				b.line(f, n, hits)
				if l.attrs["branchesToCover"] != "" {
					b.point(f, "branches", fmt.Sprint(n), b.number(l.attrs["coveredBranches"]), b.number(l.attrs["branchesToCover"]))
				}
			}
		}
	case "clover":
		root.each("file", func(src *xmlElement) {
			f := b.file(src.attrs["name"])
			for _, l := range src.child("line") {
				n := b.number(l.attrs["num"])
				if l.attrs["type"] == "cond" {
					t, fcount := b.number(l.attrs["truecount"]), b.number(l.attrs["falsecount"])
					b.line(f, n, max(t, fcount))
					covered := 0
					if t > 0 {
						covered++
					}
					if fcount > 0 {
						covered++
					}
					b.point(f, "branches", fmt.Sprint(n), covered, 2)
				} else if l.attrs["type"] == "stmt" {
					hits := b.number(l.attrs["count"])
					b.line(f, n, hits)
				} else if l.attrs["type"] == "method" {
					covered := 0
					if b.number(l.attrs["count"]) > 0 {
						covered = 1
					}
					b.point(f, "functions", fmt.Sprint(n)+":"+l.attrs["name"], covered, 1)
				} // Method declarations are not executable statement lines.
			}
		})
	case "opencover":
		b.openCover(root)
	}
	return nil
}

func (b *reportBuilder) openCover(root *xmlElement) {
	root.each("Module", func(module *xmlElement) {
		files := map[string]*fileBuilder{}
		module.each("File", func(f *xmlElement) { files[f.attrs["uid"]] = b.file(f.attrs["fullPath"]) })
		methodIndex := 0
		module.each("Method", func(method *xmlElement) {
			methodIndex++
			defaultID := ""
			for _, ref := range method.child("FileRef") {
				defaultID = ref.attrs["uid"]
			}
			for _, kind := range []string{"SequencePoint", "BranchPoint"} {
				method.each(kind, func(p *xmlElement) {
					id := p.attrs["fileid"]
					if id == "" || id == "0" {
						id = defaultID
					}
					f := files[id]
					if f == nil {
						b.err = fmt.Errorf("coverage point references an unknown file")
						return
					}
					line := b.number(p.attrs["sl"])
					if line == 0xFEEFEE {
						return
					}
					hits := b.number(p.attrs["vc"])
					if kind == "SequencePoint" {
						b.line(f, line, hits)
					} else {
						covered := 0
						if hits > 0 {
							covered = 1
						}
						b.point(f, "branches", fmt.Sprintf("%d:%s:%s", methodIndex, p.attrs["offset"], p.attrs["path"]), covered, 1)
					}
				})
			}
		})
	})
}

func (b *reportBuilder) istanbul() error {
	type location struct {
		Start struct {
			Line int `json:"line"`
		} `json:"start"`
	}
	type file struct {
		Path         string              `json:"path"`
		StatementMap map[string]location `json:"statementMap"`
		S            map[string]int      `json:"s"`
		B            map[string][]int    `json:"b"`
		F            map[string]int      `json:"f"`
	}
	var data map[string]file
	if err := json.Unmarshal([]byte(b.input.Content), &data); err != nil {
		return err
	}
	for key, v := range data {
		if v.Path == "" {
			v.Path = key
		}
		if v.StatementMap == nil || v.S == nil || len(v.StatementMap) != len(v.S) {
			return fmt.Errorf("expected Istanbul coverage-final.json with statement maps and counts")
		}
		f := b.file(v.Path)
		for id, loc := range v.StatementMap {
			hits, ok := v.S[id]
			if !ok {
				return fmt.Errorf("missing statement count")
			}
			b.line(f, loc.Start.Line, hits)
			covered := 0
			if hits > 0 {
				covered = 1
			}
			b.point(f, "statements", id, covered, 1)
		}
		if v.B != nil {
			b.point(f, "branches", "empty", 0, 0)
		}
		for id, counts := range v.B {
			for index, hits := range counts {
				if hits < 0 {
					return fmt.Errorf("negative branch hit count")
				}
				covered := 0
				if hits > 0 {
					covered = 1
				}
				b.point(f, "branches", fmt.Sprintf("%s:%d", id, index), covered, 1)
			}
		}
		if v.F != nil {
			b.point(f, "functions", "empty", 0, 0)
		}
		for id, hits := range v.F {
			if hits < 0 {
				return fmt.Errorf("negative function hit count")
			}
			covered := 0
			if hits > 0 {
				covered = 1
			}
			b.point(f, "functions", id, covered, 1)
		}
	}
	return nil
}

func (b *reportBuilder) simplecov() error {
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b.input.Content), &data); err != nil {
		return err
	}
	var sets []json.RawMessage
	if v, ok := data["coverage"]; ok {
		sets = append(sets, v)
	} else {
		for _, raw := range data {
			var suite struct {
				Coverage json.RawMessage `json:"coverage"`
			}
			if err := json.Unmarshal(raw, &suite); err != nil {
				return err
			}
			if suite.Coverage == nil {
				return fmt.Errorf("expected SimpleCov coverage or resultset JSON")
			}
			sets = append(sets, suite.Coverage)
		}
	}
	for _, raw := range sets {
		var files map[string]json.RawMessage
		if err := json.Unmarshal(raw, &files); err != nil {
			return err
		}
		for name, value := range files {
			var lines []*int
			var branches map[string]map[string]int
			if strings.HasPrefix(strings.TrimSpace(string(value)), "[") {
				if err := json.Unmarshal(value, &lines); err != nil {
					return err
				}
			} else {
				var f struct {
					Lines    []*int                    `json:"lines"`
					Branches map[string]map[string]int `json:"branches"`
				}
				if err := json.Unmarshal(value, &f); err != nil {
					return err
				}
				lines, branches = f.Lines, f.Branches
			}
			f := b.file(name)
			for i, hits := range lines {
				if hits != nil {
					b.line(f, i+1, *hits)
				}
			}
			if branches != nil {
				b.point(f, "branches", "empty", 0, 0)
			}
			for condition, paths := range branches {
				for branch, hits := range paths {
					if hits < 0 {
						return fmt.Errorf("negative branch hit count")
					}
					covered := 0
					if hits > 0 {
						covered = 1
					}
					b.point(f, "branches", condition+":"+branch, covered, 1)
				}
			}
		}
	}
	return nil
}
