package awsec2

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"pricing-catalog/internal/pipeline"
)

// TestFullOfferFile checks complete AWS offer files. It only runs when
// AWS_EC2_OFFER_FILE is set (several files can be listed like PATH entries):
//
//	AWS_EC2_OFFER_FILE=$PWD/mx-central-1.json go test ./connectors/aws/ec2/ -run TestFullOfferFile -v
func TestFullOfferFile(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("AWS_EC2_OFFER_FILE"))
	if len(paths) == 0 {
		t.Skip("set AWS_EC2_OFFER_FILE to downloaded AmazonEC2 offer files to run this test")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			checkFullOfferFile(t, path)
		})
	}
}

func checkFullOfferFile(t *testing.T, path string) {
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	want := countOffer(t, path)
	runtime.GC()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	stopWatching := watchPeakHeap()
	start := time.Now()
	snapshot, err := pipeline.BuildSnapshot(context.Background(), New(file), testBuildOptions)
	elapsed := time.Since(start)
	peakHeap := stopWatching()
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	if len(snapshot.Records) != want.priceDimensions {
		t.Fatalf("got %d records, but the file has %d in-scope price dimensions", len(snapshot.Records), want.priceDimensions)
	}

	// Every record must be selectable through PriceQuery's top-level fields.
	topLevel := make(map[string]struct{}, len(snapshot.Records))
	for _, record := range snapshot.Records {
		key := strings.Join([]string{record.SKU, record.Region, record.PurchaseModel, record.BillingUnit, record.PricingDimension}, "|")
		if _, exists := topLevel[key]; exists {
			t.Fatalf("two records share sku, region, purchase model, billing unit and pricing dimension: %s", key)
		}
		topLevel[key] = struct{}{}
	}

	purchaseModels := map[string]int{}
	charges := map[string]int{}
	regions := map[string]int{}
	zeroPrices := 0
	for _, record := range snapshot.Records {
		purchaseModels[record.PurchaseModel]++
		charges[record.BillingUnit+"/"+record.PricingDimension]++
		regions[record.Region]++
		if strings.Trim(record.Price.Amount, "0.") == "" {
			zeroPrices++
		}
	}

	t.Logf("file: %s, %d MB", filepath.Base(path), info.Size()>>20)
	t.Logf("records: %d = in-scope price dimensions in the file: %d; all unique by top-level fields", len(snapshot.Records), want.priceDimensions)
	t.Logf("in-scope products: %d; skipped products: %s", want.inScopeProducts, summarize(want.skippedProducts))
	t.Logf("in-scope price dimensions with appliesTo or a Restriction: %d", want.conditional)
	t.Logf("regions: %s", summarize(regions))
	t.Logf("charges: %s", summarize(charges))
	t.Logf("purchase models (%d): %s", len(purchaseModels), summarize(purchaseModels))
	t.Logf("zero prices: %d", zeroPrices)
	t.Logf("fetch + validate + sort: %s, peak heap in use: %d MB", elapsed.Round(time.Millisecond), peakHeap>>20)
}

type offerCounts struct {
	inScopeProducts int
	priceDimensions int            // price dimensions of in-scope products
	conditional     int            // in-scope price dimensions that only apply under conditions
	skippedProducts map[string]int // reason → number of products
}

// countOffer counts in-scope price dimensions with its own loop, to compare with the connector.
func countOffer(t *testing.T, path string) offerCounts {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var offer offerFile
	if err := json.NewDecoder(file).Decode(&offer); err != nil {
		t.Fatalf("decode offer: %v", err)
	}

	counts := offerCounts{skippedProducts: map[string]int{}}
	for _, p := range offer.Products {
		switch {
		case inScope(p):
			counts.inScopeProducts++
		case p.Attributes["group"] != "":
			counts.skippedProducts[p.ProductFamily+" ("+p.Attributes["group"]+")"]++
		case p.ProductFamily == "":
			counts.skippedProducts["(no product family)"]++
		default:
			counts.skippedProducts[p.ProductFamily]++
		}
	}

	for _, terms := range []map[string]map[string]term{offer.Terms.OnDemand, offer.Terms.Reserved} {
		for sku, skuTerms := range terms {
			if !inScope(offer.Products[sku]) {
				continue
			}
			for _, tm := range skuTerms {
				for _, d := range tm.PriceDimensions {
					counts.priceDimensions++
					if len(d.AppliesTo) > 0 || tm.TermAttributes["Restriction"] != "" {
						counts.conditional++
					}
				}
			}
		}
	}

	return counts
}

// watchPeakHeap samples the heap until the returned function is called and returns the peak in bytes.
func watchPeakHeap() func() uint64 {
	var peak uint64
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		var stats runtime.MemStats
		for {
			runtime.ReadMemStats(&stats)
			peak = max(peak, stats.HeapInuse)
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()

	return func() uint64 {
		close(done)
		<-finished
		return peak
	}
}

func summarize(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, key := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}
