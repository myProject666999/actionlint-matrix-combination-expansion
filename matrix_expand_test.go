package actionlint

import (
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v4"
)

func parseMatrixFragmentForTest(t *testing.T, src string) *Matrix {
	t.Helper()

	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 {
		t.Fatalf("invalid YAML document: %+v", n)
	}

	p := &parser{}
	m := p.parseMatrix(&Pos{Line: 1, Col: 1}, n.Content[0])
	if len(p.errors) != 0 {
		t.Fatalf("parseMatrix returned syntax errors: %v", p.errors)
	}
	return m
}

func canonicalMatrixComboForTest(c matrixCombo) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, c[k].String()))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func sortedMatrixCombosForTest(cs []matrixCombo) []string {
	ret := make([]string, 0, len(cs))
	for _, c := range cs {
		ret = append(ret, canonicalMatrixComboForTest(c))
	}
	sort.Strings(ret)
	return ret
}

func TestExpandMatrixTableDriven(t *testing.T) {
	tests := []struct {
		name               string
		yaml               string
		wantSupported      bool
		wantEnumerated     bool
		wantLen            int
		wantCombos         []string
		wantExcludeMatches []bool
		wantPartialKeys    []string
		wantNoPartialKeys  []string
	}{
		{
			name: "basic cartesian product",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        4,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", os="ubuntu"}`,
				`{node="16", os="windows"}`,
			},
			wantNoPartialKeys: []string{"os", "node"},
		},
		{
			name: "exclude subset match removes scalar combination",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
exclude:
  - os: windows
    node: 16
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        3,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", os="ubuntu"}`,
			},
			wantExcludeMatches: []bool{true},
		},
		{
			name: "exclude object subset match",
			yaml: `
os:
  - {name: Ubuntu, id: ubuntu}
  - {name: Windows, id: windows}
exclude:
  - os: {id: windows}
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        1,
			wantCombos: []string{
				`{os={"id": "ubuntu", "name": "Ubuntu"}}`,
			},
			wantExcludeMatches: []bool{true},
		},
		{
			name: "include supplements one existing combination",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
include: [{os: windows, node: 16, npm: 7}]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        4,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", npm="7", os="windows"}`,
				`{node="16", os="ubuntu"}`,
			},
			wantPartialKeys:   []string{"npm"},
			wantNoPartialKeys: []string{"os", "node"},
		},
		{
			name: "include adds a new combination",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
include: [{os: macos, node: 18}]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        5,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", os="ubuntu"}`,
				`{node="16", os="windows"}`,
				`{node="18", os="macos"}`,
			},
		},
		{
			name: "keyless original-matrix include value applies to every combination",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
include: [{experimental: true}]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        4,
			wantCombos: []string{
				`{experimental="true", node="14", os="ubuntu"}`,
				`{experimental="true", node="14", os="windows"}`,
				`{experimental="true", node="16", os="ubuntu"}`,
				`{experimental="true", node="16", os="windows"}`,
			},
			wantNoPartialKeys: []string{"experimental", "node", "os"},
		},
		{
			name: "include supplement with extra key succeeds",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
include: [{os: ubuntu, node: 16, os_version: 22}]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        4,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", os="ubuntu", os_version="22"}`,
				`{node="16", os="windows"}`,
			},
			wantPartialKeys: []string{"os_version"},
		},
		{
			name: "include conflicting original row value becomes new combination",
			yaml: `
os: [ubuntu, windows]
node: [14, 16]
include: [{os: ubuntu, node: 18}]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        5,
			wantCombos: []string{
				`{node="14", os="ubuntu"}`,
				`{node="14", os="windows"}`,
				`{node="16", os="ubuntu"}`,
				`{node="16", os="windows"}`,
				`{node="18", os="ubuntu"}`,
			},
		},
		{
			name: "same include entry matches multiple combinations and added value is overwritten later",
			yaml: `
fruit: [apple, pear]
animal: [cat, dog]
include:
  - color: green
  - color: pink
    animal: cat
  - fruit: apple
    shape: circle
  - fruit: banana
  - fruit: banana
    animal: cat
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        6,
			wantCombos: []string{
				`{animal="cat", color="pink", fruit="apple", shape="circle"}`,
				`{animal="cat", color="pink", fruit="pear"}`,
				`{animal="cat", fruit="banana"}`,
				`{animal="dog", color="green", fruit="apple", shape="circle"}`,
				`{animal="dog", color="green", fruit="pear"}`,
				`{fruit="banana"}`,
			},
		},
		{
			name: "later include does not use previously added key as a match constraint",
			yaml: `
os: [ubuntu, windows]
include:
  - os: ubuntu
    scope: special
  - scope: special
    extra: x
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        2,
			wantCombos: []string{
				`{extra="x", os="ubuntu", scope="special"}`,
				`{extra="x", os="windows", scope="special"}`,
			},
		},
		{
			name: "exclude value introduced only by include does not match base",
			yaml: `
os: [ubuntu]
include:
  - os: windows-latest
exclude:
  - os: windows-latest
`,
			wantSupported:      true,
			wantEnumerated:     true,
			wantLen:            2,
			wantExcludeMatches: []bool{false},
			wantCombos: []string{
				`{os="ubuntu"}`,
				`{os="windows-latest"}`,
			},
		},
		{
			name: "count over the GitHub limit is statically expanded while below test enumeration cap",
			yaml: `
a: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17]
b: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        17 * 16,
		},
		{
			name: "all combinations excluded",
			yaml: `
os: [ubuntu]
node: [14]
exclude:
  - os: ubuntu
    node: 14
`,
			wantSupported:      true,
			wantEnumerated:     true,
			wantLen:            0,
			wantCombos:         []string{},
			wantExcludeMatches: []bool{true},
		},
		{
			name: "empty include object is a no-op",
			yaml: `
os: [ubuntu]
include:
  - {}
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        1,
			wantCombos: []string{
				`{os="ubuntu"}`,
			},
		},
		{
			name: "empty exclude list is allowed",
			yaml: `
os: [ubuntu]
exclude: []
`,
			wantSupported:  true,
			wantEnumerated: true,
			wantLen:        1,
			wantCombos: []string{
				`{os="ubuntu"}`,
			},
		},
		{
			name: "expression row gives up static expansion",
			yaml: `
os: ${{ fromJSON(inputs.os) }}
node: [14]
`,
			wantSupported: false,
		},
		{
			name: "expression inside include gives up static expansion",
			yaml: `
os: [ubuntu]
include:
  - os: ${{ github.event_name }}
`,
			wantSupported: false,
		},
		{
			name: "mixed object and scalar values give up static expansion",
			yaml: `
os:
  - {name: Ubuntu}
  - ubuntu
`,
			wantSupported: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := parseMatrixFragmentForTest(t, tc.yaml)
			exp, ok := expandMatrix(m)
			if ok != tc.wantSupported {
				t.Fatalf("supported flag: got %v, want %v", ok, tc.wantSupported)
			}
			if !tc.wantSupported {
				if exp != nil {
					t.Fatalf("expansion must be nil when unsupported, got %#v", exp)
				}
				return
			}
			if exp.enumerated != tc.wantEnumerated {
				t.Fatalf("enumerated flag: got %v, want %v", exp.enumerated, tc.wantEnumerated)
			}
			if len(exp.combinations) != tc.wantLen {
				t.Fatalf("combination count: got %d, want %d", len(exp.combinations), tc.wantLen)
			}

			if tc.wantCombos != nil {
				got := sortedMatrixCombosForTest(exp.combinations)
				if diff := cmp.Diff(tc.wantCombos, got); diff != "" {
					t.Fatalf("expanded combinations mismatch (-want +got):\n%s", diff)
				}
			}

			if tc.wantExcludeMatches != nil {
				if m.Exclude == nil || len(m.Exclude.Combinations) != len(tc.wantExcludeMatches) {
					t.Fatalf("exclude count mismatch: matrix=%v want=%d", m.Exclude, len(tc.wantExcludeMatches))
				}
				for i, want := range tc.wantExcludeMatches {
					if got := excludeEntryMatchesBase(m, exp, m.Exclude.Combinations[i]); got != want {
						t.Errorf("exclude[%d] match: got %v, want %v", i, got, want)
					}
				}
			}

			for _, k := range tc.wantPartialKeys {
				if _, ok := exp.partialKeys[k]; !ok {
					t.Errorf("key %q must be partial; partial keys=%v", k, exp.partialKeys)
				}
			}
			for _, k := range tc.wantNoPartialKeys {
				if _, ok := exp.partialKeys[k]; ok {
					t.Errorf("key %q must not be partial", k)
				}
			}
		})
	}
}

func TestExpandMatrixProductFormulaRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20261125))

	for iter := 0; iter < 200; iter++ {
		rowCount := 1 + r.Intn(4)
		m := &Matrix{Rows: map[string]*MatrixRow{}}
		product := 1

		for i := 0; i < rowCount; i++ {
			name := fmt.Sprintf("k%d", i)
			valueCount := 1 + r.Intn(4)
			product *= valueCount
			values := make([]RawYAMLValue, 0, valueCount)
			for j := 0; j < valueCount; j++ {
				values = append(values, &RawYAMLString{Value: fmt.Sprintf("v%d", j)})
			}
			m.Rows[name] = &MatrixRow{
				Name:   &String{Value: name, Pos: &Pos{Line: i + 1, Col: 1}},
				Values: values,
			}
		}

		exp, ok := expandMatrix(m)
		if !ok {
			t.Fatalf("random matrix %d was unexpectedly unsupported: %#v", iter, m)
		}
		if exp.baseProduct.Int64() != int64(product) {
			t.Fatalf("iter %d: product formula %d != expansion product %s", iter, product, exp.baseProduct)
		}
		if len(exp.combinations) != product {
			t.Fatalf("iter %d: enumerated %d combinations != product %d", iter, len(exp.combinations), product)
		}
	}
}

func TestExpandMatrixLargeProductIsNotEnumerated(t *testing.T) {
	m := parseMatrixFragmentForTest(t, `
a: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
b: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
c: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
d: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
e: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
f: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
`)

	exp, ok := expandMatrix(m)
	if !ok {
		t.Fatal("large scalar matrix should be statically supported")
	}
	if exp.enumerated {
		t.Fatalf("10^6 combinations should not be materialized")
	}
	if exp.baseProduct.String() != "1000000" {
		t.Fatalf("unexpected product: %s", exp.baseProduct)
	}
	if exp.combinations != nil {
		t.Fatalf("large expansion must not retain combinations, got %d", len(exp.combinations))
	}
}

func TestPartialMatrixKeyMergesNullToAny(t *testing.T) {
	m := parseMatrixFragmentForTest(t, `
os: [ubuntu, windows]
include:
  - os: macos
    extra: watch
`)

	rule := NewRuleExpression(nil, nil)
	ty := rule.checkMatrix(m)

	extra, ok := ty.Props["extra"]
	if !ok {
		t.Fatalf("extra prop missing from matrix type: %s", ty.String())
	}
	if _, ok := extra.(AnyType); !ok {
		t.Fatalf("partial key type should merge with null to any, got %T (%s)", extra, extra.String())
	}

	os, ok := ty.Props["os"]
	if !ok {
		t.Fatalf("os prop missing from matrix type: %s", ty.String())
	}
	if _, ok := os.(StringType); !ok {
		t.Fatalf("persistent os key should remain string, got %T (%s)", os, os.String())
	}
}

func TestPartialMatrixKeyDiagnosticIsOptIn(t *testing.T) {
	src := []byte(`on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        os: [ubuntu, windows]
        include:
          - os: macos
            extra: watch
    steps:
      - run: echo hello
        if: matrix.extra == 'watch'
`)

	createLinter := func(warn bool) *Linter {
		l, err := NewLinter(io.Discard, &LinterOptions{
			OnRulesCreated: func(rules []Rule) []Rule {
				if warn {
					for _, r := range rules {
						if re, ok := r.(*RuleExpression); ok {
							re.WarnPartialMatrixKeys = true
						}
					}
				}
				return rules
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	project := &Project{root: t.TempDir()}

	errs, err := createLinter(false).Lint("test.yaml", src, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		if e.Kind == "matrix-partial" {
			t.Fatalf("partial matrix diagnostic must be disabled by default: %v", e)
		}
	}

	errs, err = createLinter(true).Lint("test.yaml", src, project)
	if err != nil {
		t.Fatal(err)
	}
	var found []*Error
	for _, e := range errs {
		if e.Kind == "matrix-partial" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("wanted one matrix-partial diagnostic, got %d: %v", len(found), errs)
	}
	if !strings.Contains(found[0].Message, `"extra"`) ||
		!strings.Contains(found[0].Message, "only in some expanded combinations") {
		t.Fatalf("unexpected partial diagnostic: %v", found[0])
	}
}
