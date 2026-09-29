package taxonomy_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	toolv1 "github.com/garm-ai/contracts/garm/tool/v1"
	"github.com/garm-ai/tools/taxonomy"
)

const file = "tools/taxonomy/v1/taxonomy.proto"

func names(t *testing.T, ext *toolv1.DeclSet) []string {
	t.Helper()
	var out []string
	for _, d := range ext.GetDeclared() {
		if d.GetDescription() == "" {
			t.Errorf("%q has no description; a compartment nobody can explain is one nobody reviews", d.GetName())
		}
		out = append(out, d.GetName())
	}
	return out
}

func fileOptions(t *testing.T) *descriptorpb.FileOptions {
	t.Helper()
	fd, err := protoregistry.GlobalFiles.FindFileByPath(file)
	if err != nil {
		t.Fatalf("%s is not registered: %v", file, err)
	}
	opts, ok := fd.Options().(*descriptorpb.FileOptions)
	if !ok {
		t.Fatalf("options are %T", fd.Options())
	}
	return opts
}

// The four names, exactly. A catalogue refuses a tool naming a compartment
// that is not declared, so a rename here is a tool that stops mounting in
// every deployment that adopted it.
func TestTheDeclarationsAreExactlyTheFourTheConstantsName(t *testing.T) {
	opts := fileOptions(t)

	comps, _ := proto.GetExtension(opts, toolv1.E_Compartments).(*toolv1.DeclSet)
	if got, want := names(t, comps), []string{taxonomy.CompartmentInternet, taxonomy.CompartmentGeneratedArtefacts}; !equal(got, want) {
		t.Errorf("compartments = %v, want %v", got, want)
	}

	sets, _ := proto.GetExtension(opts, toolv1.E_ToolSets).(*toolv1.DeclSet)
	if got, want := names(t, sets), []string{taxonomy.SetResearch, taxonomy.SetDocuments}; !equal(got, want) {
		t.Errorf("tool_sets = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
