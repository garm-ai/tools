package search_test

import (
	"regexp"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	metav1 "github.com/garm-ai/contracts/garm/meta/v1"
	toolv1 "github.com/garm-ai/contracts/garm/tool/v1"
	searchv1 "github.com/garm-ai/tools/search/gen/search/v1"
	"github.com/garm-ai/tools/taxonomy"
)

func searchWebPolicy(t *testing.T) *toolv1.ToolPolicy {
	t.Helper()
	sd := searchv1.File_search_v1_search_proto.Services().ByName("SearchService")
	if sd == nil {
		t.Fatal("no SearchService in the descriptor")
	}
	md := sd.Methods().ByName("SearchWeb")
	if md == nil {
		t.Fatal("no SearchWeb on SearchService")
	}
	opts, ok := md.Options().(*descriptorpb.MethodOptions)
	if !ok || !proto.HasExtension(opts, toolv1.E_Tool) {
		t.Fatal("SearchWeb carries no (garm.tool.v1.tool) annotation")
	}
	return proto.GetExtension(opts, toolv1.E_Tool).(*toolv1.ToolPolicy)
}

// The annotation is the policy. These are the values a manifest, a claims
// file and a guard are written against, so a change to any of them is a
// change to who can search the web through this tenant's credential.
func TestSearchWebIsAnnotatedAsTheDesignSays(t *testing.T) {
	p := searchWebPolicy(t)
	if p.GetName() != "search_web" {
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
		t.Errorf("approval.mode = %v; searching needs no grant", p.GetApproval().GetMode())
	}
	if p.GetAudit().GetLevel() != toolv1.Audit_LEVEL_LEDGER {
		t.Errorf("audit.level = %v, want LEVEL_LEDGER", p.GetAudit().GetLevel())
	}
	g := p.GetGuidance()
	if g.GetWhenToUse() == "" || g.GetWhenNotToUse() == "" || g.GetOnError() == "" {
		t.Error("guidance must carry when_to_use, when_not_to_use and on_error; they are prompt surface")
	}
}

// O1: a service with no owner names nobody on every card built from it, and
// a ledger row for this tool would be charged to nobody. A warning in the
// linter today and an error in a later release; web has the gap and this
// does not repeat it.
func TestTheServiceNamesWhoIsAnswerableForIt(t *testing.T) {
	sd := searchv1.File_search_v1_search_proto.Services().ByName("SearchService")
	opts, ok := sd.Options().(*descriptorpb.ServiceOptions)
	if !ok || !proto.HasExtension(opts, metav1.E_Owner) {
		t.Fatal("SearchService carries no (garm.meta.v1.owner)")
	}
	o := proto.GetExtension(opts, metav1.E_Owner).(*metav1.Owner)
	if o.GetTeam() == "" {
		t.Error("owner.team is empty; it is the unit a ledger row is charged to")
	}
	if o.GetContact() == "" {
		t.Error("owner.contact is empty; a card built from this service would name nowhere to go")
	}
}

// Every response scalar is `optional`, so omit on deny is distinguishable
// from empty (L6), and a result's url shows its origin to a caller who may
// not read the path.
func TestTheResponseRedactsTheWayTheDesignSays(t *testing.T) {
	for _, md := range []protoreflect.MessageDescriptor{
		(&searchv1.SearchWebResponse{}).ProtoReflect().Descriptor(),
		(&searchv1.Result{}).ProtoReflect().Descriptor(),
	} {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			if !fd.IsList() && !fd.HasPresence() {
				t.Errorf("%s.%s has no presence; omit on deny would be indistinguishable from empty", md.Name(), fd.Name())
			}
		}
		mopts := md.Options().(*descriptorpb.MessageOptions)
		def, _ := proto.GetExtension(mopts, toolv1.E_DefaultFieldPolicy).(*toolv1.FieldPolicy)
		if def.GetRead() != toolv1.Clearance_CLEARANCE_INTERNAL || def.GetOnDeny().GetOmit() == nil {
			t.Errorf("%s default = %v, want INTERNAL with omit on deny", md.Name(), def)
		}
	}

	fd := (&searchv1.Result{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("url"))
	opts := fd.Options().(*descriptorpb.FieldOptions)
	fp, _ := proto.GetExtension(opts, toolv1.E_FieldPolicy).(*toolv1.FieldPolicy)
	if fp.GetRead() != toolv1.Clearance_CLEARANCE_INTERNAL || fp.GetOnDeny().GetUrlOrigin() == nil {
		t.Errorf("Result.url policy = %v, want INTERNAL with url_origin on deny", fp)
	}
}

// The binding is what a garmtool service registers, so the names it carries
// are the ones garmd routes on: the FQN a manifest pins, the subject the hop
// travels, and the descriptor hash a mismatched build would change.
func TestTheBindingNamesTheToolTheWayTheDesignSays(t *testing.T) {
	if len(searchv1.SearchServiceTools) != 1 {
		t.Fatalf("SearchServiceTools has %d entries, want 1", len(searchv1.SearchServiceTools))
	}
	ref := searchv1.SearchServiceTools[0]
	if ref.FQN != "search.v1.search_web" {
		t.Errorf("FQN = %q", ref.FQN)
	}
	if ref.Subject != "search.v1.SearchService.SearchWeb" {
		t.Errorf("Subject = %q", ref.Subject)
	}
	if ref.Service != "search.v1.SearchService" {
		t.Errorf("Service = %q", ref.Service)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(searchv1.DescriptorHash) {
		t.Errorf("DescriptorHash = %q, want 64 lowercase hex characters", searchv1.DescriptorHash)
	}
}
