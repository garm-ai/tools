package web_test

import (
	"regexp"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	toolv1 "github.com/garm-ai/contracts/garm/tool/v1"
	"github.com/garm-ai/tools/taxonomy"
	webv1 "github.com/garm-ai/tools/web/gen/web/v1"
)

func fetchPagePolicy(t *testing.T) *toolv1.ToolPolicy {
	t.Helper()
	sd := webv1.File_web_v1_web_proto.Services().ByName("WebService")
	if sd == nil {
		t.Fatal("no WebService in the descriptor")
	}
	md := sd.Methods().ByName("FetchPage")
	if md == nil {
		t.Fatal("no FetchPage on WebService")
	}
	opts, ok := md.Options().(*descriptorpb.MethodOptions)
	if !ok || !proto.HasExtension(opts, toolv1.E_Tool) {
		t.Fatal("FetchPage carries no (garm.tool.v1.tool) annotation")
	}
	return proto.GetExtension(opts, toolv1.E_Tool).(*toolv1.ToolPolicy)
}

// The annotation is the policy. These are the values a manifest, a claims
// file and a guard are written against, so a change to any of them is a
// change to who can reach the web through this tenant.
func TestFetchPageIsAnnotatedAsTheDesignSays(t *testing.T) {
	p := fetchPagePolicy(t)
	if p.GetName() != "fetch_page" {
		t.Errorf("name = %q", p.GetName())
	}
	if p.GetVerb() != toolv1.Verb_VERB_READ {
		t.Errorf("verb = %v, want VERB_READ", p.GetVerb())
	}
	if p.GetMinClearance() != toolv1.Clearance_CLEARANCE_INTERNAL {
		t.Errorf("min_clearance = %v, want CLEARANCE_INTERNAL", p.GetMinClearance())
	}
	if c := p.GetCompartments(); len(c) != 1 || c[0] != taxonomy.CompartmentInternet {
		t.Errorf("compartments = %v, want [%s]", c, taxonomy.CompartmentInternet)
	}
	if s := p.GetSets(); len(s) != 1 || s[0] != taxonomy.SetResearch {
		t.Errorf("sets = %v, want [%s]", s, taxonomy.SetResearch)
	}
	e := p.GetEffects()
	if !e.GetIdempotent() || e.GetReversibility() != toolv1.Reversibility_REVERSIBILITY_FULL || !e.GetExternal() {
		t.Errorf("effects = %v, want idempotent, REVERSIBILITY_FULL, external", e)
	}
	if p.GetApproval().GetMode() != toolv1.Approval_MODE_UNSPECIFIED {
		t.Errorf("approval.mode = %v; reading a page needs no grant", p.GetApproval().GetMode())
	}
	if p.GetAudit().GetLevel() != toolv1.Audit_LEVEL_LEDGER {
		t.Errorf("audit.level = %v, want LEVEL_LEDGER", p.GetAudit().GetLevel())
	}
	g := p.GetGuidance()
	if g.GetWhenToUse() == "" || g.GetWhenNotToUse() == "" || g.GetOnError() == "" {
		t.Error("guidance must carry when_to_use, when_not_to_use and on_error; they are prompt surface")
	}
}

// Every response scalar is `optional`, so omit on deny is distinguishable
// from empty (L6), and final_url shows its origin to a caller who may not
// read the path.
func TestTheResponseRedactsTheWayTheDesignSays(t *testing.T) {
	md := (&webv1.FetchPageResponse{}).ProtoReflect().Descriptor()
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if !fd.IsList() && !fd.HasPresence() {
			t.Errorf("%s has no presence; omit on deny would be indistinguishable from empty", fd.Name())
		}
	}
	fd := fields.ByName(protoreflect.Name("final_url"))
	opts := fd.Options().(*descriptorpb.FieldOptions)
	fp, _ := proto.GetExtension(opts, toolv1.E_FieldPolicy).(*toolv1.FieldPolicy)
	if fp.GetRead() != toolv1.Clearance_CLEARANCE_INTERNAL || fp.GetOnDeny().GetUrlOrigin() == nil {
		t.Errorf("final_url policy = %v, want INTERNAL with url_origin on deny", fp)
	}
	mopts := md.Options().(*descriptorpb.MessageOptions)
	def, _ := proto.GetExtension(mopts, toolv1.E_DefaultFieldPolicy).(*toolv1.FieldPolicy)
	if def.GetRead() != toolv1.Clearance_CLEARANCE_INTERNAL || def.GetOnDeny().GetOmit() == nil {
		t.Errorf("response default = %v, want INTERNAL with omit on deny", def)
	}
}

// The binding is what a garmtool service registers, so the names it carries
// are the ones garmd routes on: the FQN a manifest pins, the subject the hop
// travels, and the descriptor hash a mismatched build would change.
func TestTheBindingNamesTheToolTheWayTheDesignSays(t *testing.T) {
	if len(webv1.WebServiceTools) != 1 {
		t.Fatalf("WebServiceTools has %d entries, want 1", len(webv1.WebServiceTools))
	}
	ref := webv1.WebServiceTools[0]
	if ref.FQN != "web.v1.fetch_page" {
		t.Errorf("FQN = %q", ref.FQN)
	}
	if ref.Subject != "web.v1.WebService.FetchPage" {
		t.Errorf("Subject = %q", ref.Subject)
	}
	if ref.Service != "web.v1.WebService" {
		t.Errorf("Service = %q", ref.Service)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(webv1.DescriptorHash) {
		t.Errorf("DescriptorHash = %q, want 64 lowercase hex characters", webv1.DescriptorHash)
	}
}
