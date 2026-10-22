package awsec2

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"pricing-catalog/pkg/catalog"
	"pricing-catalog/pkg/connector"
	"pricing-catalog/pkg/connector/testkit"
)

// Expected values are copied by hand from the AWS files in testdata.

const mxSourceURL = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/20261015191316/mx-central-1/index.json"

// fixture returns a new connector on every call; a connector consumes its reader.
func fixture(t *testing.T, name string) func() connector.Connector {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return func() connector.Connector {
		return New(bytes.NewReader(data))
	}
}

func findRecord(t *testing.T, records []catalog.PricingRecord, sku, purchaseModel, pricingDimension string) catalog.PricingRecord {
	t.Helper()

	var found []catalog.PricingRecord
	for _, record := range records {
		if record.SKU == sku && record.PurchaseModel == purchaseModel && record.PricingDimension == pricingDimension {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected one record for %s/%s/%s, got %d", sku, purchaseModel, pricingDimension, len(found))
	}
	return found[0]
}

func TestOnDemandRecordMapping(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "ondemand.json"))
	got := findRecord(t, records, "GSZZ5DDM4RZGAWK3", "on_demand", "instance_usage")

	effectiveFrom := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	want := catalog.PricingRecord{
		Provider:          "aws",
		Product:           "m7i.large",
		SKU:               "GSZZ5DDM4RZGAWK3",
		ProviderProductID: "AmazonEC2",
		ProviderSKUID:     "GSZZ5DDM4RZGAWK3",
		Region:            "mx-central-1",
		PurchaseModel:     "on_demand",
		BillingUnit:       "hour",
		PricingDimension:  "instance_usage",
		Dimensions: map[string]string{
			"operating_system": "Linux",
			"tenancy":          "Shared",
			"license_model":    "No License required",
			"pre_installed_sw": "NA",
			"capacity_status":  "Used",
		},
		Price:         catalog.Money{Amount: "0.1058400000", Currency: "USD"},
		Tier:          &catalog.Tier{StartQuantity: "0", EndQuantity: ""},
		EffectiveFrom: &effectiveFrom,
		Source:        catalog.Source{Type: "aws-price-list-bulk-api", URL: mxSourceURL},
		Attributes: demoAttributes(
			"JRTCKXETXF",
			"GSZZ5DDM4RZGAWK3.JRTCKXETXF.6YS6EN2CT7",
			"$0.10584 per On Demand Linux m7i.large Instance Hour",
			"Hrs",
		),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected record\n got: %#v\nwant: %#v", got, want)
	}
}

// demoAttributes are the attributes of GSZZ5DDM4RZGAWK3 (m7i.large, Linux, mx-central-1).
func demoAttributes(offerTermCode, rateCode, description, unit string) map[string]string {
	return map[string]string{
		"instance_family":           "General purpose",
		"vcpu":                      "2",
		"memory":                    "8 GiB",
		"physical_processor":        "Intel Xeon Scalable (Sapphire Rapids)",
		"processor_architecture":    "64-bit",
		"storage":                   "EBS only",
		"network_performance":       "Up to 12500 Megabit",
		"current_generation":        "Yes",
		"normalization_size_factor": "4",
		"usage_type":                "MXC1-BoxUsage:m7i.large",
		"operation":                 "RunInstances",
		"location":                  "Mexico (Central)",
		"location_type":             "AWS Region",
		"product_family":            "Compute Instance",
		"offer_term_code":           offerTermCode,
		"rate_code":                 rateCode,
		"price_description":         description,
		"price_unit":                unit,
	}
}

func TestReservedUpfrontRecordMapping(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "reserved.json"))
	got := findRecord(t, records, "GSZZ5DDM4RZGAWK3", "reserved_1yr_standard_partial_upfront", "upfront_fee")

	effectiveFrom := time.Date(2026, 10, 15, 19, 36, 32, 0, time.UTC)
	want := catalog.PricingRecord{
		Provider:          "aws",
		Product:           "m7i.large",
		SKU:               "GSZZ5DDM4RZGAWK3",
		ProviderProductID: "AmazonEC2",
		ProviderSKUID:     "GSZZ5DDM4RZGAWK3",
		Region:            "mx-central-1",
		PurchaseModel:     "reserved_1yr_standard_partial_upfront",
		BillingUnit:       "reservation",
		PricingDimension:  "upfront_fee",
		Dimensions: map[string]string{
			"operating_system":      "Linux",
			"tenancy":               "Shared",
			"license_model":         "No License required",
			"pre_installed_sw":      "NA",
			"capacity_status":       "Used",
			"lease_contract_length": "1yr",
			"offering_class":        "standard",
			"purchase_option":       "Partial Upfront",
		},
		Price:         catalog.Money{Amount: "292", Currency: "USD"},
		Tier:          nil,
		EffectiveFrom: &effectiveFrom,
		Source:        catalog.Source{Type: "aws-price-list-bulk-api", URL: mxSourceURL},
		Attributes: demoAttributes(
			"HU7G6KETJZ",
			"GSZZ5DDM4RZGAWK3.HU7G6KETJZ.2TG2D8R56U",
			"Upfront Fee",
			"Quantity",
		),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected record\n got: %#v\nwant: %#v", got, want)
	}
}

func TestReservedTermsKeepCommitmentAndChargeStructure(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "reserved.json"))
	if len(records) != 21 {
		t.Fatalf("expected 21 records (1 on-demand + 20 reserved charges), got %d", len(records))
	}

	const sku = "GSZZ5DDM4RZGAWK3"
	if got := findRecord(t, records, sku, "on_demand", "instance_usage"); got.Price.Amount != "0.1058400000" {
		t.Fatalf("on-demand price: got %s", got.Price.Amount)
	}

	tests := []struct {
		purchaseModel string
		lease         string
		class         string
		option        string
		hourly        string
		upfront       string // empty when the option has no upfront fee
	}{
		{"reserved_1yr_standard_no_upfront", "1yr", "standard", "No Upfront", "0.0700100000", ""},
		{"reserved_1yr_standard_partial_upfront", "1yr", "standard", "Partial Upfront", "0.0333400000", "292"},
		{"reserved_1yr_standard_all_upfront", "1yr", "standard", "All Upfront", "0.0000000000", "572"},
		{"reserved_1yr_convertible_no_upfront", "1yr", "convertible", "No Upfront", "0.0777600000", ""},
		{"reserved_1yr_convertible_partial_upfront", "1yr", "convertible", "Partial Upfront", "0.0370300000", "324"},
		{"reserved_1yr_convertible_all_upfront", "1yr", "convertible", "All Upfront", "0.0000000000", "636"},
		{"reserved_3yr_standard_no_upfront", "3yr", "standard", "No Upfront", "0.0480100000", ""},
		{"reserved_3yr_standard_partial_upfront", "3yr", "standard", "Partial Upfront", "0.0222300000", "584"},
		{"reserved_3yr_standard_all_upfront", "3yr", "standard", "All Upfront", "0.0000000000", "1098"},
		{"reserved_3yr_convertible_no_upfront", "3yr", "convertible", "No Upfront", "0.0536800000", ""},
		{"reserved_3yr_convertible_partial_upfront", "3yr", "convertible", "Partial Upfront", "0.0248500000", "653"},
		{"reserved_3yr_convertible_all_upfront", "3yr", "convertible", "All Upfront", "0.0000000000", "1280"},
	}

	for _, tt := range tests {
		t.Run(tt.purchaseModel, func(t *testing.T) {
			hourly := findRecord(t, records, sku, tt.purchaseModel, "instance_usage")
			if hourly.BillingUnit != "hour" || hourly.Price.Amount != tt.hourly {
				t.Errorf("hourly: got %s %s, want hour %s", hourly.BillingUnit, hourly.Price.Amount, tt.hourly)
			}
			if !reflect.DeepEqual(hourly.Tier, &catalog.Tier{StartQuantity: "0"}) {
				t.Errorf("hourly tier: got %#v", hourly.Tier)
			}

			wantTerm := map[string]string{"lease_contract_length": tt.lease, "offering_class": tt.class, "purchase_option": tt.option}
			for key, value := range wantTerm {
				if hourly.Dimensions[key] != value {
					t.Errorf("dimension %s: got %q, want %q", key, hourly.Dimensions[key], value)
				}
			}

			var upfront []catalog.PricingRecord
			for _, record := range records {
				if record.PurchaseModel == tt.purchaseModel && record.PricingDimension == "upfront_fee" {
					upfront = append(upfront, record)
				}
			}
			if tt.upfront == "" {
				if len(upfront) != 0 {
					t.Fatalf("expected no upfront fee, got %d records", len(upfront))
				}
				return
			}
			if len(upfront) != 1 {
				t.Fatalf("expected one upfront fee, got %d records", len(upfront))
			}
			if upfront[0].BillingUnit != "reservation" || upfront[0].Price.Amount != tt.upfront || upfront[0].Tier != nil {
				t.Errorf("upfront: got %s %s tier=%#v, want reservation %s without tier",
					upfront[0].BillingUnit, upfront[0].Price.Amount, upfront[0].Tier, tt.upfront)
			}
		})
	}
}

// AWS lists the EBS optimization surcharge in the Compute Instance family with
// an hourly unit. It must not be returned as the instance's usage price.
func TestEBSOptimizationIsNotInstanceUsage(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "ondemand.json"))

	got := findRecord(t, records, "TFXK2Y2NZM7RHDH6", "on_demand", "ebs_optimized")
	if got.Product != "g4dn.xlarge" || got.Region != "us-east-1-bos-1" || got.BillingUnit != "hour" ||
		got.Price != (catalog.Money{Amount: "0.0000000000", Currency: "USD"}) ||
		got.Attributes["usage_type"] != "BOS1-EBSOptimized:g4dn.xlarge" {
		t.Fatalf("unexpected record: %#v", got)
	}

	c, err := catalog.New("test", records, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	usage := c.Prices(catalog.PriceQuery{
		Product:          "g4dn.xlarge",
		Region:           "us-east-1-bos-1",
		PurchaseModel:    "on_demand",
		PricingDimension: "instance_usage",
	})
	if len(usage) != 0 {
		t.Fatalf("instance_usage query returned %d records, including %s", len(usage), usage[0].SKU)
	}
}

func TestOnDemandKeepsPriceDistinctions(t *testing.T) {
	records := testkit.AssertConnectorContract(t, fixture(t, "ondemand.json"))
	if len(records) != 11 {
		t.Fatalf("expected 11 on-demand records, got %d", len(records))
	}

	dims := func(os, tenancy, license, software, capacity string) map[string]string {
		return map[string]string{
			"operating_system": os,
			"tenancy":          tenancy,
			"license_model":    license,
			"pre_installed_sw": software,
			"capacity_status":  capacity,
		}
	}
	const noLicense = "No License required"

	tests := []struct {
		name       string
		sku        string
		region     string
		product    string
		amount     string
		dimensions map[string]string
	}{
		{"linux shared", "GSZZ5DDM4RZGAWK3", "mx-central-1", "m7i.large", "0.1058400000", dims("Linux", "Shared", noLicense, "NA", "Used")},
		{"unused capacity reservation", "YRU9VJCJFA6JQE69", "mx-central-1", "m7i.large", "0.1058400000", dims("Linux", "Shared", noLicense, "NA", "UnusedCapacityReservation")},
		{"allocated capacity reservation is zero", "R7YDMKJK6U2NFQFZ", "mx-central-1", "m7i.large", "0.0000000000", dims("Linux", "Shared", noLicense, "NA", "AllocatedCapacityReservation")},
		{"rhel dedicated", "4KHDTG64WYEVVBYP", "mx-central-1", "m7i.large", "0.1452200000", dims("RHEL", "Dedicated", noLicense, "NA", "Used")},
		{"host tenancy is zero", "UFU3542XWYW86U9Y", "mx-central-1", "m7i.large", "0.0000000000", dims("Linux", "Host", noLicense, "NA", "Used")},
		{"windows with sql standard", "WYRDFVDQUBYDUDS4", "mx-central-1", "m7i.large", "0.6778400000", dims("Windows", "Shared", noLicense, "SQL Std", "Used")},
		{"windows bring your own license", "RGUR7PJCX5VW97DY", "mx-central-1", "m7i.large", "0.1058400000", dims("Windows", "Shared", "Bring your own license", "NA", "Used")},
		{"bare metal", "38BK8SD7NGC5K8CQ", "mx-central-1", "m8gd.metal-48xl", "11.6236800000", dims("Linux", "Shared", noLicense, "NA", "Used")},
		{"local zone", "YC3UPGSJSF657SZT", "us-east-1-bos-1", "m5.2xlarge", "0.4800000000", dims("Linux", "Shared", noLicense, "NA", "Used")},
		{"local zone windows sql unused reservation", "CA6QVKDWB22K83MH", "us-east-1-bos-1", "m5.2xlarge", "1.8080000000", dims("Windows", "Shared", noLicense, "SQL Std", "UnusedCapacityReservation")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findRecord(t, records, tt.sku, "on_demand", "instance_usage")

			if got.Region != tt.region || got.Product != tt.product {
				t.Errorf("region/product: got %s/%s, want %s/%s", got.Region, got.Product, tt.region, tt.product)
			}
			if got.Price != (catalog.Money{Amount: tt.amount, Currency: "USD"}) {
				t.Errorf("price: got %#v, want %s USD", got.Price, tt.amount)
			}
			if !reflect.DeepEqual(got.Dimensions, tt.dimensions) {
				t.Errorf("dimensions: got %#v, want %#v", got.Dimensions, tt.dimensions)
			}
			wantURL := "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/20261015191316/" + tt.region + "/index.json"
			if got.Source.URL != wantURL {
				t.Errorf("source url: got %s, want %s", got.Source.URL, wantURL)
			}
		})
	}
}
