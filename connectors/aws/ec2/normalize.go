package awsec2

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"pricing-catalog/pkg/catalog"
)

const (
	provider         = "aws"
	defaultOfferCode = "AmazonEC2"
	sourceType       = "aws-price-list-bulk-api"
	sourceURLFormat  = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/%s/%s/%s/index.json"

	termTypeOnDemand = "OnDemand"
	termTypeReserved = "Reserved"

	purchaseModelOnDemand = "on_demand"
)

// Other families in the EC2 offer (EBS, Dedicated Hosts, NAT gateways, CPU
// credits, ...) are separate pricing domains and are skipped.
var computeFamilies = map[string]bool{
	"Compute Instance":              true,
	"Compute Instance (bare metal)": true,
}

type attributeMapping struct {
	aws       string
	canonical string
}

// Attributes that change the price. They become dimensions, which are part of
// the record identity.
var dimensionAttributes = []attributeMapping{
	{"operatingSystem", "operating_system"},
	{"tenancy", "tenancy"},
	{"licenseModel", "license_model"},
	{"preInstalledSw", "pre_installed_sw"},
	{"capacitystatus", "capacity_status"},
}

var reservedTermAttributes = []attributeMapping{
	{"LeaseContractLength", "lease_contract_length"},
	{"OfferingClass", "offering_class"},
	{"PurchaseOption", "purchase_option"},
}

// Attributes kept for information only.
var descriptiveAttributes = []attributeMapping{
	{"instanceFamily", "instance_family"},
	{"vcpu", "vcpu"},
	{"memory", "memory"},
	{"gpu", "gpu"},
	{"physicalProcessor", "physical_processor"},
	{"processorArchitecture", "processor_architecture"},
	{"storage", "storage"},
	{"networkPerformance", "network_performance"},
	{"currentGeneration", "current_generation"},
	{"normalizationSizeFactor", "normalization_size_factor"},
	{"usagetype", "usage_type"},
	{"operation", "operation"},
	{"location", "location"},
	{"locationType", "location_type"},
}

// inScope skips non-compute products and the free-trial SKU, which is a
// promotion without a region rather than a list price.
func inScope(p product) bool {
	return computeFamilies[p.ProductFamily] && p.Attributes["group"] != "FreeTrial"
}

// normalize joins terms to products by SKU and returns one record per price
// dimension. Keys are sorted so a broken file always reports the same error.
func normalize(ctx context.Context, offer offerFile) ([]catalog.PricingRecord, error) {
	offerCode := offer.OfferCode
	if offerCode == "" {
		offerCode = defaultOfferCode
	}
	if offerCode != defaultOfferCode {
		return nil, fmt.Errorf("offer code %q is not supported: expected %s", offerCode, defaultOfferCode)
	}

	termSets := []struct {
		termType string
		terms    map[string]map[string]term
	}{
		{termTypeOnDemand, offer.Terms.OnDemand},
		{termTypeReserved, offer.Terms.Reserved},
	}

	var records []catalog.PricingRecord
	for _, set := range termSets {
		for _, sku := range slices.Sorted(maps.Keys(set.terms)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			product, ok := offer.Products[sku]
			if !ok {
				return nil, fmt.Errorf("sku %s: %s terms reference a product missing from the offer", sku, set.termType)
			}
			if product.SKU != "" && product.SKU != sku {
				return nil, fmt.Errorf("sku %s: product entry has sku %q, which does not match", sku, product.SKU)
			}
			if !inScope(product) {
				continue
			}

			terms := set.terms[sku]
			if len(terms) == 0 {
				return nil, fmt.Errorf("sku %s: %s terms are empty", sku, set.termType)
			}
			for _, termKey := range slices.Sorted(maps.Keys(terms)) {
				term := terms[termKey]
				if term.SKU != "" && term.SKU != sku {
					return nil, fmt.Errorf("sku %s, term %s: term has sku %q, which does not match", sku, termKey, term.SKU)
				}
				if len(term.PriceDimensions) == 0 {
					return nil, fmt.Errorf("sku %s, term %s: term has no price dimensions", sku, termKey)
				}
				if restriction := term.TermAttributes["Restriction"]; restriction != "" {
					return nil, fmt.Errorf("sku %s, term %s: conditional term (Restriction %q) is not supported", sku, termKey, restriction)
				}

				purchaseModel, termDimensions, err := purchaseModelFor(set.termType, term)
				if err != nil {
					return nil, fmt.Errorf("sku %s, term %s: %w", sku, termKey, err)
				}

				for _, rateCode := range slices.Sorted(maps.Keys(term.PriceDimensions)) {
					if appliesTo := term.PriceDimensions[rateCode].AppliesTo; len(appliesTo) > 0 {
						return nil, fmt.Errorf("sku %s, rate %s: conditional price (applies to %d SKUs) is not supported", sku, rateCode, len(appliesTo))
					}
					record, err := newRecord(offerCode, offer.Version, sku, product, term, term.PriceDimensions[rateCode], purchaseModel, termDimensions)
					if err == nil {
						// Validated here too so the error names the SKU and rate.
						err = catalog.ValidateRecord(record)
					}
					if err != nil {
						return nil, fmt.Errorf("sku %s, rate %s: %w", sku, rateCode, err)
					}
					records = append(records, record)
				}
			}
		}
	}

	return records, nil
}

// purchaseModelFor returns "on_demand" or, for reserved terms,
// "reserved_<lease>_<class>_<option>" together with those values as
// dimensions. PriceQuery only filters on top-level fields, so the reserved
// term has to be part of the purchase model to be queryable.
func purchaseModelFor(termType string, t term) (string, map[string]string, error) {
	switch termType {
	case termTypeOnDemand:
		return purchaseModelOnDemand, nil, nil
	case termTypeReserved:
		parts := []string{"reserved"}
		dimensions := make(map[string]string, len(reservedTermAttributes))
		for _, m := range reservedTermAttributes {
			value := strings.TrimSpace(t.TermAttributes[m.aws])
			if value == "" {
				return "", nil, fmt.Errorf("reserved term has no %s", m.aws)
			}
			parts = append(parts, strings.ToLower(strings.ReplaceAll(value, " ", "_")))
			dimensions[m.canonical] = value
		}
		return strings.Join(parts, "_"), dimensions, nil
	default:
		return "", nil, fmt.Errorf("unsupported term type %q", termType)
	}
}

func newRecord(
	offerCode string,
	version string,
	sku string,
	product product,
	term term,
	dimension priceDimension,
	purchaseModel string,
	termDimensions map[string]string,
) (catalog.PricingRecord, error) {
	instanceType := product.Attributes["instanceType"]
	if instanceType == "" {
		return catalog.PricingRecord{}, errors.New("product has no instanceType")
	}
	region := product.Attributes["regionCode"]
	if region == "" {
		return catalog.PricingRecord{}, errors.New("product has no regionCode")
	}

	billingUnit, pricingDimension, err := charge(dimension.Unit, product.Attributes)
	if err != nil {
		return catalog.PricingRecord{}, err
	}
	money, err := price(dimension.PricePerUnit)
	if err != nil {
		return catalog.PricingRecord{}, err
	}
	effectiveFrom, err := effectiveDate(term.EffectiveDate)
	if err != nil {
		return catalog.PricingRecord{}, err
	}

	dimensions := mapAttributes(product.Attributes, dimensionAttributes)
	maps.Copy(dimensions, termDimensions)

	attributes := mapAttributes(product.Attributes, descriptiveAttributes)
	attributes["product_family"] = product.ProductFamily
	attributes["offer_term_code"] = term.OfferTermCode
	attributes["rate_code"] = dimension.RateCode
	attributes["price_description"] = dimension.Description
	attributes["price_unit"] = dimension.Unit

	return catalog.PricingRecord{
		Provider:          provider,
		Product:           instanceType,
		SKU:               sku,
		ProviderProductID: offerCode,
		ProviderSKUID:     sku,
		Region:            region,
		PurchaseModel:     purchaseModel,
		BillingUnit:       billingUnit,
		PricingDimension:  pricingDimension,
		Dimensions:        dimensions,
		Price:             money,
		Tier:              tier(dimension),
		EffectiveFrom:     effectiveFrom,
		Source: catalog.Source{
			Type: sourceType,
			URL:  sourceURL(offerCode, version, region),
		},
		Attributes: attributes,
	}, nil
}

// charge classifies a price. "Quantity" is the upfront fee of a reservation.
// "Hrs" is either instance usage (operation RunInstances...) or the EBS
// optimization surcharge, which AWS lists in the same product family. Anything
// else is an error rather than a guess.
func charge(unit string, attributes map[string]string) (billingUnit, pricingDimension string, err error) {
	switch unit {
	case "Quantity":
		return "reservation", "upfront_fee", nil
	case "Hrs":
		switch {
		case usageKind(attributes["usagetype"]) == "EBSOptimized":
			return "hour", "ebs_optimized", nil
		case strings.HasPrefix(attributes["operation"], "RunInstances"):
			return "hour", "instance_usage", nil
		default:
			return "", "", fmt.Errorf("unsupported hourly charge: usagetype %q, operation %q", attributes["usagetype"], attributes["operation"])
		}
	default:
		return "", "", fmt.Errorf("unsupported price unit %q", unit)
	}
}

// usageKind returns the charge part of a usage type, e.g. "BoxUsage" for
// "MXC1-BoxUsage:m7i.large".
func usageKind(usageType string) string {
	kind, _, _ := strings.Cut(usageType, ":")
	if i := strings.LastIndex(kind, "-"); i >= 0 {
		kind = kind[i+1:]
	}
	return kind
}

// price keeps the amount exactly as published, preferring USD.
func price(perUnit map[string]string) (catalog.Money, error) {
	if amount, ok := perUnit["USD"]; ok {
		return catalog.Money{Amount: amount, Currency: "USD"}, nil
	}

	switch len(perUnit) {
	case 0:
		return catalog.Money{}, errors.New("price dimension has no price")
	case 1:
		for currency, amount := range perUnit {
			return catalog.Money{Amount: amount, Currency: currency}, nil
		}
	}

	return catalog.Money{}, fmt.Errorf("price dimension has several currencies and no USD: %v", slices.Sorted(maps.Keys(perUnit)))
}

// tier keeps the AWS range. "Inf" means no upper bound.
func tier(dimension priceDimension) *catalog.Tier {
	if dimension.BeginRange == "" && dimension.EndRange == "" {
		return nil
	}

	end := dimension.EndRange
	if end == "Inf" {
		end = ""
	}
	return &catalog.Tier{StartQuantity: dimension.BeginRange, EndQuantity: end}
}

func effectiveDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("invalid effectiveDate %q: %w", value, err)
	}
	t = t.UTC()
	return &t, nil
}

func sourceURL(offerCode, version, region string) string {
	if version == "" {
		version = "current"
	}
	return fmt.Sprintf(sourceURLFormat, offerCode, version, region)
}

func mapAttributes(source map[string]string, mappings []attributeMapping) map[string]string {
	out := make(map[string]string, len(mappings))
	for _, m := range mappings {
		if value := source[m.aws]; value != "" {
			out[m.canonical] = value
		}
	}
	return out
}
