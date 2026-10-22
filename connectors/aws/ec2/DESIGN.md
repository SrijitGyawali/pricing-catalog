# AWS EC2 connector

Converts the AWS Price List offer file for Amazon EC2 into canonical pricing
records for compute instances: On-Demand and Reserved Instance prices, for
every location in the file.

## Source

AWS Price List Bulk API price list files for the `AmazonEC2` offer. They are
public JSON files, so no AWS credentials are needed.

```text
https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/region_index.json
https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/<version>/<region>/index.json
```

The files are published by AWS and versioned, so each record can point to the
exact file it came from. I did not use the Price List Query API or the SDK
calls because they need AWS credentials and return the same data.

AWS docs:
[Price List overview](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/price-changes.html),
[Getting price list files manually](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-the-aws-price-list-bulk-api-fetching-price-list-files-manually.html),
[Finding prices in the price list files](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/finding-prices-in-service-price-list-files.html).

Fixtures and measurements use price list version `20261015191316`.

## AWS pricing model

- `products` maps a SKU to its attributes. A SKU is one combination of
  instance type, location, operating system, tenancy, licence model,
  preinstalled software and capacity status.
- `terms.OnDemand` has one term per SKU. `terms.Reserved` has up to 12 per
  SKU: 1yr or 3yr, standard or convertible, No, Partial or All Upfront.
- Each term has price dimensions. `Hrs` is an hourly rate and `Quantity` is a
  one-time upfront fee. Partial and All Upfront have both; the All Upfront
  hourly rate is 0.
- The `Compute Instance` family also contains EBS optimization surcharges
  (`usagetype` `...EBSOptimized:<type>`, operation `Hourly`), also priced in
  `Hrs`. Instance usage always has an operation starting with `RunInstances`.
- Prices are USD decimal strings, compute ranges are `0` to `Inf`, and each
  term has an `effectiveDate`.
- Locations include Regions, Local Zones, Wavelength Zones and GovCloud.
- Identifiers: the SKU, the `offerTermCode` of each term, the `rateCode` of
  each price (`<SKU>.<offerTermCode>.<rate>`), `usagetype` and `operation`
  (as used in AWS billing), and the file `version`.

## Scope

In scope: `Compute Instance` and `Compute Instance (bare metal)` products,
On-Demand and Reserved terms, all operating systems, tenancies, licences and
capacity statuses, and every location.

Skipped:

- Other product families in the EC2 offer: EBS, Dedicated Hosts, data
  transfer, NAT gateways, load balancers, CPU credits and fees. They are
  separate pricing domains, and EBS and data transfer are separate tasks in
  this repo.
- The global free-trial SKU (`group: FreeTrial`). It has no region and applies
  to a list of other SKUs. AWS notes the price list is not a complete source
  for Free Tier offers.

Not in this source: Spot prices (AWS: the catalog doesn't include EC2 Spot
Instances) and Savings Plans (a separate offer).

## Mapping

One record per price dimension.

| Field | Value | Example |
|---|---|---|
| Provider | `aws` | `aws` |
| Product | instance type | `m7i.large` |
| SKU, ProviderSKUID | AWS SKU | `GSZZ5DDM4RZGAWK3` |
| ProviderProductID | offer code | `AmazonEC2` |
| Region | `regionCode` | `mx-central-1` |
| PurchaseModel | `on_demand` or `reserved_<lease>_<class>_<option>` | `reserved_1yr_standard_partial_upfront` |
| BillingUnit, PricingDimension | `hour`, `instance_usage` for instance usage; `hour`, `ebs_optimized` for the EBS optimization surcharge; `reservation`, `upfront_fee` for `Quantity` | `hour`, `instance_usage` |
| Price | amount as published | `0.1058400000` USD |
| Tier | `beginRange` to `endRange`, `Inf` means no upper bound | `0` to open |
| EffectiveFrom | term `effectiveDate` | `2026-10-01T00:00:00Z` |
| Source | versioned regional file URL | `.../AmazonEC2/20261015191316/mx-central-1/index.json` |
| Dimensions | `operating_system`, `tenancy`, `license_model`, `pre_installed_sw`, `capacity_status`, and for reserved terms `lease_contract_length`, `offering_class`, `purchase_option` | |
| Attributes | instance details (`vcpu`, `memory`, `physical_processor`, ...), `usage_type`, `operation`, `location`, `offer_term_code`, `rate_code`, `price_description`, `price_unit` | |

Decisions:

- Anything that changes the price is a dimension, because `RecordKey`
  includes dimensions but not attributes.
- The reserved term is part of the purchase model because `PriceQuery` only
  filters on top-level fields. Otherwise "1yr No Upfront" prices could not be
  queried. SKU, region, purchase model, billing unit and pricing dimension
  identify every record; this was checked on 491,812 real prices.
- Product is the instance type, so consumers can query by it.
- Hourly charges are classified by usage type and operation, not only by unit.
  Otherwise the EBS optimization surcharge would be returned as the
  instance's usage price.
- The upfront fee and the hourly rate stay separate records. I don't compute
  an effective hourly price because that needs assumptions about term length
  and usage.
- Prices are copied as strings and never parsed as floats. Zero prices are
  kept: All Upfront hourly rates, allocated capacity reservations (billed
  through the reservation) and Host tenancy (billed through the Dedicated
  Host).

## Assumptions

- The input is one AmazonEC2 offer file. An empty offer code is treated as
  AmazonEC2; other offer codes are rejected.
- AWS states that if the price list and the pricing page differ, the pricing
  page applies.
- If the file has no `version`, the source URL uses `current`.
- USD is preferred. A single other currency is kept as is (China partition,
  not tested).
- AWS publishes no end date, so `EffectiveTo` is not set.

## Errors

Skipped without an error: out-of-scope families, products without a family,
the free-trial SKU.

The fetch fails on:

- input that is not exactly one JSON document (`connector.SourceError`);
- another offer code;
- a term whose product is missing or stored under another SKU;
- an in-scope SKU with an empty term list, or a term without price dimensions;
- a conditional price (`appliesTo`) or term (`Restriction`);
- a compute product without region or instance type;
- a missing or invalid price, an unknown unit, an hourly charge that is neither
  instance usage nor EBS optimization, or an invalid date;
- a reserved term without lease, class or payment option.

Errors name the SKU and term or rate code, for example
`sku SKU1, rate SKU1.JRTCKXETXF.6YS6EN2CT7: unsupported price unit "GB-Mo"`.

A file with nothing in scope returns no records, so the pipeline fails with
`ErrEmptyCatalog`.

## Not represented

- The link between a reservation's upfront fee and hourly rate. The two
  records share `sku`, `purchase_model` and `offer_term_code`.
- Conditional allowances such as the free trial.
- Reservation size flexibility (`normalization_size_factor` is kept as an
  attribute).
- Less common product attributes such as `clockSpeed` or `processorFeatures`.

## Tests

- `connector_test.go`: contract test, one On-Demand and one upfront record
  checked field by field, 10 On-Demand variants, an EBS optimization charge
  that must not appear as instance usage, all 12 Reserved offers of one SKU.
- `hardening_test.go`: scope on real data, 19 malformed inputs, a term without
  prices among valid SKUs, data after the JSON document, nil reader,
  cancellation, JSON key order, empty scope.
- `normalize_test.go`: each mapping rule.
- `fullfile_test.go`: optional check against complete AWS files.

Fixtures are unmodified excerpts of the AWS files (see `testdata/README.md`).
Expected values were copied by hand from the AWS JSON.

Complete files, version `20261015191316`, on an 8-core laptop with 7.7 GB RAM:

| File | Records | Time | Peak heap |
|---|---|---|---|
| us-east-1 (459 MB) | 392,357 | 45-72 s | about 2.6-2.7 GB |
| mx-central-1 (100 MB) | 97,923 | 10-19 s | about 870 MB |
| us-east-1-bos-1 (3.7 MB) | 1,532 | under 0.2 s | under 20 MB |

In each file the number of records equals an independent count of in-scope
price dimensions. us-east-1 has 108 and us-east-1-bos-1 has 24 EBS
optimization charges, classified as `ebs_optimized`. No file has an empty
term, a conditional price or an unknown hourly charge in scope.

## Limitations

- The whole file is decoded at once. Decoding alone took about 2.6 times the
  file size in memory (259 MB for the 100 MB mx-central-1 file); the full
  pipeline peaks are in the table above. Regional files work; the 8.5 GB
  all-regions file does not. Streaming the `terms` section would reduce the
  decoding cost, but the pipeline still holds all records.
- The connector reads a file. Downloading and refreshing are left to the
  pipeline.
- Only `group: FreeTrial` is skipped by name. Other compute products are
  mapped if they pass the checks under Errors; usage types under a
  `RunInstances` operation are treated as instance usage.

## Suggested model changes

1. Dimension filters in `PriceQuery`, so the reserved term would not need to be
   part of the purchase model.
2. An offer ID that links charges bought together.
3. A way to represent conditional allowances such as free tiers.
4. Currency in `RecordKey`.

## Running the tests

```bash
go test ./connectors/aws/ec2/ -v
make check
AWS_EC2_OFFER_FILE=/path/to/mx-central-1.json go test ./connectors/aws/ec2/ -run TestFullOfferFile -v
```
