package main

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// Response is of type APIGatewayProxyResponse since we're leveraging the
// AWS Lambda Proxy Request functionality (default behavior)
//
// https://serverless.com/framework/docs/providers/aws/events/apigateway/#lambda-proxy-integration
type Response events.APIGatewayProxyResponse

// Handler is our lambda handler invoked by the `lambda.Start` function call
func Handler(ctx context.Context) (response Response, err error) {

	res, err := Worker()
	if err != nil {
		return
	}

	jsonRes, err := json.Marshal(res)
	if err != nil {
		return
	}

	response = Response{
		StatusCode:      200,
		IsBase64Encoded: false,
		Body:            string(jsonRes),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	}

	return
}

func main() {
	lambda.Start(Handler)
}
