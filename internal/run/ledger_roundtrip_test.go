package run

import (
	"reflect"
	"testing"

	"github.com/greatliontech/gofresh"
)

func TestLedgerNativeCompanionRoundTrip(t *testing.T) {
	want := gofresh.TestVariantLedger{
		BindingStrategy: "bindingparse@2",
		BaseFiles:       []gofresh.TestVariantFileHeader{{File: "x.go", Bindings: &gofresh.TestVariantFileBindings{Package: "p", References: []string{"a"}, Imports: []gofresh.TestVariantImport{{Name: "a", Path: "example.com/a"}}}}},
		Declarations:    []gofresh.TestVariantDeclaration{{File: "x_test.go", Kind: "method", Name: "Method", Receiver: "T", Hash: "hash", Package: "p", References: []string{"a", "b"}}},
		FileHeaders:     []gofresh.TestVariantFileHeader{{File: "x_test.go", Hash: "header", Embedded: true, Bindings: &gofresh.TestVariantFileBindings{Package: "p", References: []string{"b"}, Imports: []gofresh.TestVariantImport{{Name: "b", Path: "example.com/b"}}}}},
	}
	// A new shared ledger field must receive a nonzero anchor, so the companion
	// adapter cannot silently drop new diff semantics.
	for _, row := range []any{want.Declarations[0], want.FileHeaders[0]} {
		v := reflect.ValueOf(row)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).IsZero() {
				t.Fatalf("unexercised ledger field %s", v.Type().Field(i).Name)
			}
		}
	}
	encoded, err := EncodeLedger(LedgerFromGofresh(want))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeLedger(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.ToGofresh(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger changed: %+v, want %+v", got, want)
	}
}
