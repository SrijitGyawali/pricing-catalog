# Fixtures

Excerpts of the official AWS EC2 price list files, version `20261015191316`:

- https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/20261015191316/mx-central-1/index.json
- https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/20261015191316/us-east-1-bos-1/index.json

Products and terms are copied unchanged; only the number of SKUs is reduced.

- `ondemand.json`: 11 SKUs with On-Demand terms. `m7i.large` with different
  OS, tenancy, licence, software and capacity status, a bare-metal SKU, two
  instance SKUs from the Boston Local Zone, and a Boston EBS optimization
  charge (`TFXK2Y2NZM7RHDH6`).
- `reserved.json`: `m7i.large` Linux (`GSZZ5DDM4RZGAWK3`) with its On-Demand
  term and all 12 Reserved terms.
- `mixed.json`: the same SKU plus products that must be skipped: storage, CPU
  credits, a fee, a NAT gateway, a Dedicated Host, a product without a family
  and the free-trial SKU.
