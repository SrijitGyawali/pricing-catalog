package awsec2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pricing-catalog/internal/pipeline"
	"pricing-catalog/pkg/connector"
	"pricing-catalog/pkg/connector/testkit"
)

var testBuildOptions = pipeline.BuildOptions{Version: "test", GeneratedAt: time.Unix(0, 0).UTC()}

func TestMixedOfferKeepsOnlyComputePrices(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "mixed.json"))
	if len(records) != 1 {
		t.Fatalf("expected only the compute SKU's on-demand record, got %d records", len(records))
	}

	got := records[0]
	if got.SKU != "GSZZ5DDM4RZGAWK3" || got.PurchaseModel != "on_demand" || got.Price.Amount != "0.1058400000" {
		t.Fatalf("unexpected record: %s %s %s", got.SKU, got.PurchaseModel, got.Price.Amount)
	}
}

// The same data with JSON keys in another order must give the same snapshot.
func TestOutputDoesNotDependOnSourceOrder(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "ondemand.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var decoded any
	if err := json.Unmarshal(original, &decoded); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(decoded) // encoding/json writes object keys sorted
	if err != nil {
		t.Fatal(err)
	}

	before := func(data []byte, a, b string) bool {
		return bytes.Index(data, []byte(a)) < bytes.Index(data, []byte(b))
	}
	if !before(original, `"GSZZ5DDM4RZGAWK3"`, `"38BK8SD7NGC5K8CQ"`) || before(reordered, `"GSZZ5DDM4RZGAWK3"`, `"38BK8SD7NGC5K8CQ"`) {
		t.Fatal("re-encoding did not change the product order; the test would prove nothing")
	}

	build := func(data []byte) []byte {
		t.Helper()
		snapshot, err := pipeline.BuildSnapshot(context.Background(), New(bytes.NewReader(data)), testBuildOptions)
		if err != nil {
			t.Fatalf("build snapshot: %v", err)
		}
		out, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if !bytes.Equal(build(original), build(reordered)) {
		t.Fatal("snapshot changed when only the order of the source JSON changed")
	}
}

// minimalOffer is a small synthetic offer. Each malformed-input case breaks one part of it.
func minimalOffer() map[string]any {
	return map[string]any{
		"offerCode": "AmazonEC2",
		"version":   "20261015191316",
		"products": map[string]any{
			"SKU1": map[string]any{
				"sku":           "SKU1",
				"productFamily": "Compute Instance",
				"attributes": map[string]any{
					"instanceType":    "m7i.large",
					"regionCode":      "mx-central-1",
					"operatingSystem": "Linux",
					"tenancy":         "Shared",
					"usagetype":       "MXC1-BoxUsage:m7i.large",
					"operation":       "RunInstances",
				},
			},
		},
		"terms": map[string]any{
			"OnDemand": map[string]any{
				"SKU1": map[string]any{
					"SKU1.JRTCKXETXF": map[string]any{
						"offerTermCode": "JRTCKXETXF",
						"sku":           "SKU1",
						"effectiveDate": "2026-10-01T00:00:00Z",
						"priceDimensions": map[string]any{
							"SKU1.JRTCKXETXF.6YS6EN2CT7": map[string]any{
								"rateCode":     "SKU1.JRTCKXETXF.6YS6EN2CT7",
								"unit":         "Hrs",
								"beginRange":   "0",
								"endRange":     "Inf",
								"pricePerUnit": map[string]any{"USD": "0.1058400000"},
							},
						},
						"termAttributes": map[string]any{},
					},
				},
			},
		},
	}
}

func productOf(o map[string]any) map[string]any {
	return o["products"].(map[string]any)["SKU1"].(map[string]any)
}

func attributesOf(o map[string]any) map[string]any {
	return productOf(o)["attributes"].(map[string]any)
}

func onDemandTermOf(o map[string]any) map[string]any {
	return o["terms"].(map[string]any)["OnDemand"].(map[string]any)["SKU1"].(map[string]any)["SKU1.JRTCKXETXF"].(map[string]any)
}

func priceDimensionOf(o map[string]any) map[string]any {
	return onDemandTermOf(o)["priceDimensions"].(map[string]any)["SKU1.JRTCKXETXF.6YS6EN2CT7"].(map[string]any)
}

func encode(t *testing.T, o map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMinimalOfferIsValid(t *testing.T) {
	records, err := New(bytes.NewReader(encode(t, minimalOffer()))).Fetch(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("the unmodified synthetic offer must produce one record, got %d, %v", len(records), err)
	}
}

func TestFetchRejectsMalformedOffers(t *testing.T) {
	const rate = "sku SKU1, rate SKU1.JRTCKXETXF.6YS6EN2CT7"

	tests := []struct {
		name   string
		raw    string // used instead of minimalOffer when set
		modify func(o map[string]any)
		want   []string // substrings the error must contain
		source bool     // the error must be a connector.SourceError
	}{
		{name: "not json", raw: "not json", want: []string{"decode offer file"}, source: true},
		{name: "truncated json", raw: `{"products": {"SKU1": `, want: []string{"decode offer file"}, source: true},
		{name: "empty input", raw: " ", want: []string{"decode offer file"}, source: true},
		{name: "wrong document shape", raw: `["AmazonEC2"]`, want: []string{"decode offer file"}, source: true},
		{
			name:   "offer for another service",
			modify: func(o map[string]any) { o["offerCode"] = "AmazonRDS" },
			want:   []string{`offer code "AmazonRDS"`},
		},
		{
			name:   "term without product",
			modify: func(o map[string]any) { delete(o["products"].(map[string]any), "SKU1") },
			want:   []string{"sku SKU1", "missing from the offer"},
		},
		{
			name:   "product stored under another sku",
			modify: func(o map[string]any) { productOf(o)["sku"] = "SKU2" },
			want:   []string{"sku SKU1", `"SKU2"`, "does not match"},
		},
		{
			name:   "term stored under another sku",
			modify: func(o map[string]any) { onDemandTermOf(o)["sku"] = "SKU2" },
			want:   []string{"term SKU1.JRTCKXETXF", `"SKU2"`, "does not match"},
		},
		{
			name:   "missing region",
			modify: func(o map[string]any) { delete(attributesOf(o), "regionCode") },
			want:   []string{rate, "regionCode"},
		},
		{
			name:   "missing instance type",
			modify: func(o map[string]any) { delete(attributesOf(o), "instanceType") },
			want:   []string{rate, "instanceType"},
		},
		{
			name:   "missing price",
			modify: func(o map[string]any) { priceDimensionOf(o)["pricePerUnit"] = map[string]any{} },
			want:   []string{rate, "no price"},
		},
		{
			name:   "negative price",
			modify: func(o map[string]any) { priceDimensionOf(o)["pricePerUnit"] = map[string]any{"USD": "-0.1"} },
			want:   []string{rate, "price.amount"},
		},
		{
			name:   "unknown unit",
			modify: func(o map[string]any) { priceDimensionOf(o)["unit"] = "GB-Mo" },
			want:   []string{rate, `unsupported price unit "GB-Mo"`},
		},
		{
			name:   "invalid effective date",
			modify: func(o map[string]any) { onDemandTermOf(o)["effectiveDate"] = "yesterday" },
			want:   []string{rate, "invalid effectiveDate"},
		},
		{
			name: "unknown hourly charge",
			modify: func(o map[string]any) {
				attributesOf(o)["usagetype"] = "MXC1-SomethingNew:m7i.large"
				attributesOf(o)["operation"] = "Hourly"
			},
			want: []string{rate, "unsupported hourly charge"},
		},
		{
			name:   "price that applies only to other skus",
			modify: func(o map[string]any) { priceDimensionOf(o)["appliesTo"] = []any{"SKU2"} },
			want:   []string{rate, "conditional price"},
		},
		{
			name: "restricted term",
			modify: func(o map[string]any) {
				onDemandTermOf(o)["termAttributes"] = map[string]any{"Restriction": "Limited SKU Usage"}
			},
			want: []string{"term SKU1.JRTCKXETXF", "conditional term"},
		},
		{
			name: "empty terms for a sku",
			modify: func(o map[string]any) {
				o["terms"].(map[string]any)["OnDemand"].(map[string]any)["SKU1"] = map[string]any{}
			},
			want: []string{"sku SKU1", "terms are empty"},
		},
		{
			name: "reserved term without payment option",
			modify: func(o map[string]any) {
				o["terms"].(map[string]any)["Reserved"] = map[string]any{
					"SKU1": map[string]any{
						"SKU1.4NA7Y494T4": map[string]any{
							"offerTermCode": "4NA7Y494T4",
							"sku":           "SKU1",
							"effectiveDate": "2026-10-15T19:36:29Z",
							"priceDimensions": map[string]any{
								"SKU1.4NA7Y494T4.6YS6EN2CT7": map[string]any{
									"rateCode":     "SKU1.4NA7Y494T4.6YS6EN2CT7",
									"unit":         "Hrs",
									"beginRange":   "0",
									"endRange":     "Inf",
									"pricePerUnit": map[string]any{"USD": "0.0700100000"},
								},
							},
							"termAttributes": map[string]any{"LeaseContractLength": "1yr", "OfferingClass": "standard"},
						},
					},
				}
			},
			want: []string{"term SKU1.4NA7Y494T4", "PurchaseOption"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.raw)
			if tt.raw == "" {
				o := minimalOffer()
				tt.modify(o)
				data = encode(t, o)
			}

			records, err := New(bytes.NewReader(data)).Fetch(context.Background())
			if err == nil {
				t.Fatalf("expected an error, got %d records", len(records))
			}
			t.Logf("error: %v", err)
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}

			var sourceErr *connector.SourceError
			if errors.As(err, &sourceErr) != tt.source {
				t.Errorf("SourceError: got %v, want %v (error: %v)", !tt.source, tt.source, err)
			}
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The other ten valid SKUs in the file must not hide one term without prices.
func TestFetchRejectsTermWithoutPrices(t *testing.T) {
	for _, mode := range []string{"empty", "missing", "null"} {
		t.Run(mode, func(t *testing.T) {
			var o map[string]any
			if err := json.Unmarshal(readFixture(t, "ondemand.json"), &o); err != nil {
				t.Fatal(err)
			}
			term := o["terms"].(map[string]any)["OnDemand"].(map[string]any)["GSZZ5DDM4RZGAWK3"].(map[string]any)["GSZZ5DDM4RZGAWK3.JRTCKXETXF"].(map[string]any)
			switch mode {
			case "empty":
				term["priceDimensions"] = map[string]any{}
			case "missing":
				delete(term, "priceDimensions")
			case "null":
				term["priceDimensions"] = nil
			}

			records, err := New(bytes.NewReader(encode(t, o))).Fetch(context.Background())
			if err == nil {
				t.Fatalf("accepted an incomplete offer with %d records", len(records))
			}
			if !strings.Contains(err.Error(), "term GSZZ5DDM4RZGAWK3.JRTCKXETXF: term has no price dimensions") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFetchRejectsDataAfterOffer(t *testing.T) {
	tests := []struct {
		name    string
		suffix  string
		wantErr bool
	}{
		{"second json document", "\n{}", true},
		{"garbage", "\nBROKEN_DOWNLOAD", true},
		{"trailing whitespace", "\n\n \t\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := append(readFixture(t, "ondemand.json"), tt.suffix...)
			records, err := New(bytes.NewReader(data)).Fetch(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %d records, error %v; wantErr %v", len(records), err, tt.wantErr)
			}

			var sourceErr *connector.SourceError
			if tt.wantErr && !errors.As(err, &sourceErr) {
				t.Fatalf("expected a SourceError, got %v", err)
			}
		})
	}
}

func TestFetchRejectsNilReader(t *testing.T) {
	_, err := New(nil).Fetch(context.Background())

	var sourceErr *connector.SourceError
	if !errors.As(err, &sourceErr) || sourceErr.Connector != "aws-ec2" {
		t.Fatalf("expected an aws-ec2 SourceError, got %v", err)
	}
}

// unreadableReader reports a test error if anything reads from it.
type unreadableReader struct{ t *testing.T }

func (r unreadableReader) Read([]byte) (int, error) {
	r.t.Error("the source was read after the context was cancelled")
	return 0, io.EOF
}

func TestFetchStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := New(unreadableReader{t}).Fetch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch: expected context.Canceled, got %v", err)
	}

	// normalize also checks for cancellation while it runs.
	data, err := os.ReadFile(filepath.Join("testdata", "ondemand.json"))
	if err != nil {
		t.Fatal(err)
	}
	var offer offerFile
	if err := json.Unmarshal(data, &offer); err != nil {
		t.Fatal(err)
	}
	if _, err := normalize(ctx, offer); !errors.Is(err, context.Canceled) {
		t.Fatalf("normalize: expected context.Canceled, got %v", err)
	}
}

// A file with nothing in scope must fail the build, not produce an empty catalog.
func TestOfferWithoutComputePricesFailsTheBuild(t *testing.T) {
	o := minimalOffer()
	productOf(o)["productFamily"] = "Storage"
	data := encode(t, o)

	records, err := New(bytes.NewReader(data)).Fetch(context.Background())
	if err != nil || len(records) != 0 {
		t.Fatalf("connector: expected no records and no error, got %d, %v", len(records), err)
	}

	_, err = pipeline.BuildSnapshot(context.Background(), New(bytes.NewReader(data)), testBuildOptions)
	if !errors.Is(err, pipeline.ErrEmptyCatalog) {
		t.Fatalf("pipeline: expected ErrEmptyCatalog, got %v", err)
	}
}
