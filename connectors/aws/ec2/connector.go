// Package awsec2 maps the AWS Price List offer file for Amazon EC2 to
// canonical pricing records.
package awsec2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"pricing-catalog/pkg/catalog"
	"pricing-catalog/pkg/connector"
)

const connectorName = "aws-ec2"

// Connector reads one AmazonEC2 offer file, for example
// https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/us-east-1/index.json.
type Connector struct {
	reader io.Reader
}

func New(reader io.Reader) Connector {
	return Connector{reader: reader}
}

func (c Connector) Name() string { return connectorName }

func (c Connector) Fetch(ctx context.Context) ([]catalog.PricingRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.reader == nil {
		return nil, sourceError(errors.New("offer file reader is nil"))
	}

	decoder := json.NewDecoder(c.reader)
	var offer offerFile
	if err := decoder.Decode(&offer); err != nil {
		return nil, sourceError(fmt.Errorf("decode offer file: %w", err))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, sourceError(errors.New("offer file contains more than one JSON document"))
		}
		return nil, sourceError(fmt.Errorf("decode data after offer file: %w", err))
	}

	return normalize(ctx, offer)
}

func sourceError(err error) error {
	return &connector.SourceError{Connector: connectorName, Err: err}
}
