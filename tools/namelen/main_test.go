package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan(test *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "accepts descriptive names",
			src:  "package p\nfunc run(page string) (count int) {\n\tindex := 0\n\tfor key, value := range page {\n\t}\n\treturn index\n}\n",
		},
		{
			name: "blank identifier is allowed",
			src:  "package p\nfunc run() {\n\t_, name := get()\n\t_ = name\n}\n",
		},
		{
			name: "flags a short local and a short parameter",
			src:  "package p\nfunc run(a int) {\n\tb := a\n\t_ = b\n}\n",
			want: []string{"a", "b"},
		},
		{
			name: "flags receivers, struct fields and type parameters",
			src:  "package p\ntype box[K any] struct{ s string }\nfunc (r *box[K]) get() string { return r.s }\n",
			want: []string{"K", "s", "r"},
		},
		{
			name: "exempts loop variables but flags import aliases",
			src:  "package p\nimport f \"fmt\"\nfunc run(xs []int) {\n\tfor i := range xs {\n\t\t_ = i\n\t}\n\tf.Println()\n}\n",
			want: []string{"f"},
		},
		{
			name: "exempts test handles and closure parameters",
			src: "package p\nimport \"testing\"\nfunc TestX(t *testing.T) {\n\tt.Run(\"x\", func(t *testing.T) {\n\t\tt.Log()\n\t})\n}\n" +
				"func BenchmarkX(b *testing.B) {}\nfunc helper(tb testing.TB) {}\n" +
				"func use(apply func(value int) int) int { return apply(1) }\n",
		},
		{
			name: "flags a short local even inside a closure",
			src:  "package p\nfunc run(xs []int) {\n\tsort(func(i, j int) bool {\n\t\tn := xs[i]\n\t\t_ = n\n\t\treturn n < xs[j]\n\t})\n\tx := 1\n\t_ = x\n}\n",
			want: []string{"n", "x"},
		},
		{
			name: "ignores assignment to existing names",
			src:  "package p\nvar a int\nfunc run() {\n\ta = 2\n\tname := 1\n\t_ = name\n}\n",
			want: []string{"a"},
		},
	}
	for _, tt := range tests {
		test.Run(tt.name, func(subtest *testing.T) {
			dir := subtest.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(tt.src), 0o600); err != nil {
				subtest.Fatal(err)
			}
			findings, err := scan(dir)
			if err != nil {
				subtest.Fatal(err)
			}
			if len(findings) != len(tt.want) {
				subtest.Fatalf("got %d findings, want %d:\n%s", len(findings), len(tt.want), strings.Join(findings, "\n"))
			}
			for index, want := range tt.want {
				if !strings.Contains(findings[index], "\""+want+"\"") {
					subtest.Errorf("finding %d = %q, want name %q", index, findings[index], want)
				}
			}
		})
	}
}
