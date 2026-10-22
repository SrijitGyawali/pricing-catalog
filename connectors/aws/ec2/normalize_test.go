package awsec2

import (
	"reflect"
	"testing"
	"time"

	"pricing-catalog/pkg/catalog"
)

func TestTier(t *testing.T) {
	tests := []struct {
		name      string
		dimension priceDimension
		want      *catalog.Tier
	}{
		{"unbounded range", priceDimension{BeginRange: "0", EndRange: "Inf"}, &catalog.Tier{StartQuantity: "0"}},
		{"bounded range", priceDimension{BeginRange: "0", EndRange: "750"}, &catalog.Tier{StartQuantity: "0", EndQuantity: "750"}},
		{"no range", priceDimension{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tier(tt.dimension); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestInScope(t *testing.T) {
	tests := []struct {
		name    string
		product product
		want    bool
	}{
		{"compute instance", product{ProductFamily: "Compute Instance"}, true},
		{"bare metal", product{ProductFamily: "Compute Instance (bare metal)"}, true},
		{"dedicated host", product{ProductFamily: "Dedicated Host"}, false},
		{"ebs storage", product{ProductFamily: "Storage"}, false},
		{"missing product family", product{}, false},
		{"free trial", product{ProductFamily: "Compute Instance", Attributes: map[string]string{"group": "FreeTrial"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inScope(tt.product); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCharge(t *testing.T) {
	instance := map[string]string{"usagetype": "MXC1-BoxUsage:m7i.large", "operation": "RunInstances"}
	windows := map[string]string{"usagetype": "MXC1-BoxUsage:m7i.large", "operation": "RunInstances:0002"}
	ebsOptimized := map[string]string{"usagetype": "BOS1-EBSOptimized:g4dn.xlarge", "operation": "Hourly"}
	unknown := map[string]string{"usagetype": "MXC1-SomethingNew:m7i.large", "operation": "Hourly"}

	tests := []struct {
		name          string
		unit          string
		attributes    map[string]string
		wantUnit      string
		wantDimension string
		wantErr       bool
	}{
		{"instance usage", "Hrs", instance, "hour", "instance_usage", false},
		{"windows instance usage", "Hrs", windows, "hour", "instance_usage", false},
		{"ebs optimization", "Hrs", ebsOptimized, "hour", "ebs_optimized", false},
		{"upfront fee", "Quantity", instance, "reservation", "upfront_fee", false},
		{"unknown hourly charge", "Hrs", unknown, "", "", true},
		{"unknown unit", "GB-Mo", instance, "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			billingUnit, pricingDimension, err := charge(tt.unit, tt.attributes)
			if (err != nil) != tt.wantErr || billingUnit != tt.wantUnit || pricingDimension != tt.wantDimension {
				t.Fatalf("got %q/%q/%v", billingUnit, pricingDimension, err)
			}
		})
	}
}

func TestUsageKind(t *testing.T) {
	tests := map[string]string{
		"MXC1-BoxUsage:m7i.large":       "BoxUsage",
		"BoxUsage:c4.2xlarge":           "BoxUsage",
		"BOS1-EBSOptimized:g4dn.xlarge": "EBSOptimized",
		"USE1-WL1-ATL1-BoxUsage:t3.xl":  "BoxUsage",
		"":                              "",
	}
	for usageType, want := range tests {
		if got := usageKind(usageType); got != want {
			t.Errorf("usageKind(%q) = %q, want %q", usageType, got, want)
		}
	}
}

func TestPurchaseModelFor(t *testing.T) {
	model, dimensions, err := purchaseModelFor("OnDemand", term{})
	if err != nil || model != "on_demand" || dimensions != nil {
		t.Fatalf("on-demand: got %q, %#v, %v", model, dimensions, err)
	}

	reserved := term{TermAttributes: map[string]string{
		"LeaseContractLength": "3yr",
		"OfferingClass":       "convertible",
		"PurchaseOption":      "All Upfront",
	}}
	model, dimensions, err = purchaseModelFor("Reserved", reserved)
	if err != nil || model != "reserved_3yr_convertible_all_upfront" {
		t.Fatalf("reserved: got %q, %v", model, err)
	}
	wantDimensions := map[string]string{
		"lease_contract_length": "3yr",
		"offering_class":        "convertible",
		"purchase_option":       "All Upfront",
	}
	if !reflect.DeepEqual(dimensions, wantDimensions) {
		t.Fatalf("reserved dimensions: got %#v", dimensions)
	}

	incomplete := term{TermAttributes: map[string]string{"LeaseContractLength": "1yr", "OfferingClass": "standard"}}
	if _, _, err := purchaseModelFor("Reserved", incomplete); err == nil {
		t.Fatal("expected an error for a reserved term without PurchaseOption")
	}

	if _, _, err := purchaseModelFor("Spot", term{}); err == nil {
		t.Fatal("expected an error for an unsupported term type")
	}
}

func TestPrice(t *testing.T) {
	tests := []struct {
		name    string
		perUnit map[string]string
		want    catalog.Money
		wantErr bool
	}{
		{"usd kept verbatim", map[string]string{"USD": "0.1058400000"}, catalog.Money{Amount: "0.1058400000", Currency: "USD"}, false},
		{"usd preferred", map[string]string{"CNY": "0.7", "USD": "0.1"}, catalog.Money{Amount: "0.1", Currency: "USD"}, false},
		{"single other currency", map[string]string{"CNY": "0.7"}, catalog.Money{Amount: "0.7", Currency: "CNY"}, false},
		{"no price", map[string]string{}, catalog.Money{}, true},
		{"several currencies without usd", map[string]string{"CNY": "0.7", "EUR": "0.1"}, catalog.Money{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := price(tt.perUnit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error: got %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEffectiveDate(t *testing.T) {
	got, err := effectiveDate("2026-10-01T00:00:00Z")
	if err != nil || !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v, %v", got, err)
	}

	if got, err := effectiveDate(""); got != nil || err != nil {
		t.Fatalf("missing date must stay missing, got %v, %v", got, err)
	}

	if _, err := effectiveDate("1 October 2026"); err == nil {
		t.Fatal("expected an error for an invalid date")
	}
}

func TestSourceURL(t *testing.T) {
	if got := sourceURL("AmazonEC2", "20261015191316", "mx-central-1"); got != mxSourceURL {
		t.Fatalf("got %s", got)
	}

	want := "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/mx-central-1/index.json"
	if got := sourceURL("AmazonEC2", "", "mx-central-1"); got != want {
		t.Fatalf("unversioned offer: got %s", got)
	}
}
