package auth

import (
	"context"
	"testing"

	"cs-team/store"
)

func adminCtx(ctx context.Context, name string) context.Context {
	ctx = context.WithValue(ctx, ctxKey{}, name)
	return context.WithValue(ctx, ctxAdmin{}, true)
}

func TestBulkExportImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	actx := adminCtx(ctx, "anna")
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(actx, "bob", "bobgeheim1", false, []string{DefaultGroup}); err != nil {
		t.Fatal(err)
	}

	d := a.ExportBulk(actx, BulkExportOpts{PW: true})
	if len(d.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(d.Users))
	}
	if d.Users["bob"].Hash == "" {
		t.Fatal("PW=true but hash missing")
	}
	if len(d.Groups) == 0 {
		t.Fatal("expected groups for admin export")
	}

	// Ohne PW: Hash muss leer sein.
	d2 := a.ExportBulk(actx, BulkExportOpts{})
	if d2.Users["bob"].Hash != "" {
		t.Fatal("PW=false but hash present")
	}

	// Re-Import auf eine frische Instanz: altes Passwort muss gueltig bleiben.
	b := New(store.NewMem())
	bctx := adminCtx(ctx, "anna")
	res := b.ImportBulk(bctx, BulkDump{Users: d.Users, Groups: d.Groups}, BulkImportOpts{Create: true, Update: true})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if res.Created != 2 {
		t.Fatalf("expected 2 created, got %d (errors=%v)", res.Created, res.Errors)
	}
	if _, ok := b.verify(ctx, "bob", "bobgeheim1"); !ok {
		t.Fatal("bob password did not survive import")
	}
}

func TestBulkImportGenPW(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	actx := adminCtx(ctx, "anna")
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	d := BulkDump{Users: map[string]Account{
		"carla": {Groups: []string{DefaultGroup}}, // kein Hash: simuliert Export von Windows-Host oder AD/LDAP
	}}
	res := a.ImportBulk(actx, d, BulkImportOpts{Create: true})
	if len(res.Errors) == 0 {
		t.Fatal("expected error without genpw and without hash")
	}
	res2 := a.ImportBulk(actx, d, BulkImportOpts{Create: true, GenPW: true})
	if len(res2.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res2.Errors)
	}
	pw, ok := res2.GenPW["carla"]
	if !ok || pw == "" {
		t.Fatal("expected a generated password for carla")
	}
	if _, ok := a.verify(ctx, "carla", pw); !ok {
		t.Fatal("generated password does not work")
	}
}

func TestBulkImportNonAdminForbidden(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	res := a.ImportBulk(ctx, BulkDump{Users: map[string]Account{"x": {}}}, BulkImportOpts{Create: true, GenPW: true})
	if len(res.Errors) == 0 || res.Created != 0 {
		t.Fatalf("expected forbidden without admin context, got %+v", res)
	}
}
