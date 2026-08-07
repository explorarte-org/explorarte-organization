package contextprovider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

type fakeRepository struct { entries map[string]memory.Entry; listed []memory.Entry }
func (f *fakeRepository) CreateCandidate(context.Context,memory.CreateCandidateCommand)(memory.Entry,bool,error){ return memory.Entry{},false,errors.New("not implemented") }
func (f *fakeRepository) Get(_ context.Context,org,id string)(memory.Entry,error){ entry,ok:=f.entries[id]; if !ok||entry.OrganizationID!=org{return memory.Entry{},memory.ErrEntryNotFound}; return entry,nil }
func (f *fakeRepository) Save(context.Context,memory.SaveCommand)(memory.Entry,error){ return memory.Entry{},errors.New("not implemented") }
func (f *fakeRepository) List(context.Context,memory.ListFilter)([]memory.Entry,error){ return append([]memory.Entry(nil),f.listed...),nil }
func (f *fakeRepository) ListApproved(context.Context,memory.ApprovedFilter)([]memory.Entry,error){ return append([]memory.Entry(nil),f.listed...),nil }

func approvedEntry(id,role string,now time.Time) memory.Entry {
	reviewed:=now.Add(time.Minute)
	return memory.Entry{ID:id,OrganizationID:"explorarte",RoleID:role,Category:"incident_learning",Problem:"A real failure was observed.",Correction:"Use the verified corrective procedure.",SourceKind:memory.SourceOperational,SourceRunID:42,EvidenceRefs:[]memory.EvidenceRef{{Reference:"evidence:42",Digest:"abc"}},Status:memory.StatusApproved,ProposedBy:role,ReviewerID:"empresa/human",Admission:memory.AdmissionAttestation{DataClass:memory.DataOrganizational,AttestedBy:role,SourceBoundary:"organization",EvidenceRef:"admission:42",AttestedAt:now.Add(-time.Minute)},Revision:2,CreatedAt:now,UpdatedAt:reviewed,ReviewedAt:&reviewed}
}

func TestListApprovedProducesUntrustedNonGrantingMemory(t *testing.T){
	now:=time.Date(2026,8,7,4,0,0,0,time.UTC); role:="ingenieria_ia/orquestador"; entry:=approvedEntry("mem-1",role,now)
	repo:=&fakeRepository{entries:map[string]memory.Entry{entry.ID:entry},listed:[]memory.Entry{entry}}; provider,err:=New(repo,"explorarte",5); if err!=nil{t.Fatal(err)}
	records,err:=provider.ListApproved(context.Background(),contextengine.BuildRequest{OrganizationID:"explorarte",ActorRoleID:role}); if err!=nil{t.Fatal(err)}; if len(records)!=1{t.Fatalf("records=%d",len(records))}
	record:=records[0]; if record.Kind!=contextengine.SourceApprovedMemory||record.AuthorityTier!=contextengine.TierApprovedMemory{t.Fatalf("identity=%+v",record)}; if record.InstructionClass!=contextengine.InstructionData||record.TrustClass!=contextengine.TrustUntrusted||record.MayGrantCapabilities{t.Fatalf("boundary=%+v",record)}; if err:=contextengine.ValidateSourceMetadata(record);err!=nil{t.Fatal(err)}
}

func TestSimulationProvenanceIsRendered(t *testing.T){
	now:=time.Date(2026,8,7,4,0,0,0,time.UTC); role:="ingenieria_ia/orquestador"; entry:=approvedEntry("sim-1",role,now); entry.SourceKind=memory.SourceSimulation
	record,err:=sourceRecord(entry); if err!=nil{t.Fatal(err)}
	var body renderedMemory; if err:=json.Unmarshal(record.Content,&body);err!=nil{t.Fatal(err)}; if body.SourceKind!=memory.SourceSimulation{t.Fatalf("source kind=%s",body.SourceKind)}
}

func TestListApprovedRejectsRepositoryScopeLeak(t *testing.T){
	now:=time.Date(2026,8,7,4,0,0,0,time.UTC); entry:=approvedEntry("mem-1","marketing/estratega_crecimiento",now); repo:=&fakeRepository{entries:map[string]memory.Entry{entry.ID:entry},listed:[]memory.Entry{entry}}; provider,_:=New(repo,"explorarte",5)
	if _,err:=provider.ListApproved(context.Background(),contextengine.BuildRequest{OrganizationID:"explorarte",ActorRoleID:"ingenieria_ia/orquestador"});err==nil{t.Fatal("role leak accepted")}
}
func TestListApprovedRejectsOrganizationMismatch(t *testing.T){ provider,_:=New(&fakeRepository{entries:map[string]memory.Entry{}},"explorarte",5); if _,err:=provider.ListApproved(context.Background(),contextengine.BuildRequest{OrganizationID:"other",ActorRoleID:"ingenieria_ia/orquestador"});err==nil{t.Fatal("org mismatch accepted")} }
func TestValidateVersionDetectsDeprecationAndContentDrift(t *testing.T){
	now:=time.Date(2026,8,7,4,0,0,0,time.UTC); role:="ingenieria_ia/orquestador"; entry:=approvedEntry("mem-1",role,now); repo:=&fakeRepository{entries:map[string]memory.Entry{entry.ID:entry},listed:[]memory.Entry{entry}}; provider,_:=New(repo,"explorarte",5); records,err:=provider.ListApproved(context.Background(),contextengine.BuildRequest{OrganizationID:"explorarte",ActorRoleID:role});if err!=nil{t.Fatal(err)}; expected:=records[0];if err:=provider.ValidateVersion(context.Background(),expected);err!=nil{t.Fatal(err)}
	deprecated:=entry;deprecated.Status=memory.StatusDeprecated;deprecated.Revision++;deprecated.UpdatedAt=deprecated.UpdatedAt.Add(time.Minute);repo.entries[entry.ID]=deprecated;if err:=provider.ValidateVersion(context.Background(),expected);err==nil{t.Fatal("deprecation not detected")}
	changed:=entry;changed.Correction="Different correction.";repo.entries[entry.ID]=changed;if err:=provider.ValidateVersion(context.Background(),expected);err==nil{t.Fatal("content drift not detected")}
}
func TestProviderNeverMapsForbiddenDataClasses(t *testing.T){for _,class:=range []memory.DataClass{memory.DataClinical,memory.DataSecret}{if _,err:=mapDataClass(class);err==nil{t.Fatalf("class %s mapped",class)}}}
