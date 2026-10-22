package awsec2

// offerFile is an AWS Price List offer file for AmazonEC2. Only the fields
// used by the connector are decoded. Prices stay strings.
type offerFile struct {
	FormatVersion   string             `json:"formatVersion"`
	OfferCode       string             `json:"offerCode"`
	Version         string             `json:"version"`
	PublicationDate string             `json:"publicationDate"`
	Products        map[string]product `json:"products"`
	Terms           offerTerms         `json:"terms"`
}

type product struct {
	SKU           string            `json:"sku"`
	ProductFamily string            `json:"productFamily"`
	Attributes    map[string]string `json:"attributes"`
}

// offerTerms maps SKU -> "<SKU>.<offerTermCode>" -> term.
type offerTerms struct {
	OnDemand map[string]map[string]term `json:"OnDemand"`
	Reserved map[string]map[string]term `json:"Reserved"`
}

type term struct {
	OfferTermCode   string                    `json:"offerTermCode"`
	SKU             string                    `json:"sku"`
	EffectiveDate   string                    `json:"effectiveDate"`
	PriceDimensions map[string]priceDimension `json:"priceDimensions"`
	TermAttributes  map[string]string         `json:"termAttributes"`
}

type priceDimension struct {
	RateCode     string            `json:"rateCode"`
	Description  string            `json:"description"`
	BeginRange   string            `json:"beginRange"`
	EndRange     string            `json:"endRange"`
	Unit         string            `json:"unit"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
	AppliesTo    []string          `json:"appliesTo"`
}
