// Code generated by smithy-go-codegen DO NOT EDIT.

package ec2

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	internalauth "github.com/aws/aws-sdk-go-v2/internal/auth"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"time"
)

// Creates a Spot Instance request. For more information, see Spot Instance
// requests (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/spot-requests.html)
// in the Amazon EC2 User Guide for Linux Instances. We strongly discourage using
// the RequestSpotInstances API because it is a legacy API with no planned
// investment. For options for requesting Spot Instances, see Which is the best
// Spot request method to use? (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/spot-best-practices.html#which-spot-request-method-to-use)
// in the Amazon EC2 User Guide for Linux Instances.
func (c *Client) RequestSpotInstances(ctx context.Context, params *RequestSpotInstancesInput, optFns ...func(*Options)) (*RequestSpotInstancesOutput, error) {
	if params == nil {
		params = &RequestSpotInstancesInput{}
	}

	result, metadata, err := c.invokeOperation(ctx, "RequestSpotInstances", params, optFns, c.addOperationRequestSpotInstancesMiddlewares)
	if err != nil {
		return nil, err
	}

	out := result.(*RequestSpotInstancesOutput)
	out.ResultMetadata = metadata
	return out, nil
}

// Contains the parameters for RequestSpotInstances.
type RequestSpotInstancesInput struct {

	// The user-specified name for a logical grouping of requests. When you specify an
	// Availability Zone group in a Spot Instance request, all Spot Instances in the
	// request are launched in the same Availability Zone. Instance proximity is
	// maintained with this parameter, but the choice of Availability Zone is not. The
	// group applies only to requests for Spot Instances of the same instance type. Any
	// additional Spot Instance requests that are specified with the same Availability
	// Zone group name are launched in that same Availability Zone, as long as at least
	// one instance from the group is still active. If there is no active instance
	// running in the Availability Zone group that you specify for a new Spot Instance
	// request (all instances are terminated, the request is expired, or the maximum
	// price you specified falls below current Spot price), then Amazon EC2 launches
	// the instance in any Availability Zone where the constraint can be met.
	// Consequently, the subsequent set of Spot Instances could be placed in a
	// different zone from the original request, even if you specified the same
	// Availability Zone group. Default: Instances are launched in any available
	// Availability Zone.
	AvailabilityZoneGroup *string

	// Deprecated.
	BlockDurationMinutes *int32

	// Unique, case-sensitive identifier that you provide to ensure the idempotency of
	// the request. For more information, see How to Ensure Idempotency (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Run_Instance_Idempotency.html)
	// in the Amazon EC2 User Guide for Linux Instances.
	ClientToken *string

	// Checks whether you have the required permissions for the action, without
	// actually making the request, and provides an error response. If you have the
	// required permissions, the error response is DryRunOperation . Otherwise, it is
	// UnauthorizedOperation .
	DryRun *bool

	// The maximum number of Spot Instances to launch. Default: 1
	InstanceCount *int32

	// The behavior when a Spot Instance is interrupted. The default is terminate .
	InstanceInterruptionBehavior types.InstanceInterruptionBehavior

	// The instance launch group. Launch groups are Spot Instances that launch
	// together and terminate together. Default: Instances are launched and terminated
	// individually
	LaunchGroup *string

	// The launch specification.
	LaunchSpecification *types.RequestSpotLaunchSpecification

	// The maximum price per unit hour that you are willing to pay for a Spot
	// Instance. We do not recommend using this parameter because it can lead to
	// increased interruptions. If you do not specify this parameter, you will pay the
	// current Spot price. If you specify a maximum price, your instances will be
	// interrupted more frequently than if you do not specify this parameter.
	SpotPrice *string

	// The key-value pair for tagging the Spot Instance request on creation. The value
	// for ResourceType must be spot-instances-request , otherwise the Spot Instance
	// request fails. To tag the Spot Instance request after it has been created, see
	// CreateTags (https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateTags.html)
	// .
	TagSpecifications []types.TagSpecification

	// The Spot Instance request type. Default: one-time
	Type types.SpotInstanceType

	// The start date of the request. If this is a one-time request, the request
	// becomes active at this date and time and remains active until all instances
	// launch, the request expires, or the request is canceled. If the request is
	// persistent, the request becomes active at this date and time and remains active
	// until it expires or is canceled. The specified start date and time cannot be
	// equal to the current date and time. You must specify a start date and time that
	// occurs after the current date and time.
	ValidFrom *time.Time

	// The end date of the request, in UTC format (YYYY-MM-DDTHH:MM:SSZ).
	//   - For a persistent request, the request remains active until the ValidUntil
	//   date and time is reached. Otherwise, the request remains active until you cancel
	//   it.
	//   - For a one-time request, the request remains active until all instances
	//   launch, the request is canceled, or the ValidUntil date and time is reached.
	//   By default, the request is valid for 7 days from the date the request was
	//   created.
	ValidUntil *time.Time

	noSmithyDocumentSerde
}

// Contains the output of RequestSpotInstances.
type RequestSpotInstancesOutput struct {

	// The Spot Instance requests.
	SpotInstanceRequests []types.SpotInstanceRequest

	// Metadata pertaining to the operation's result.
	ResultMetadata middleware.Metadata

	noSmithyDocumentSerde
}

func (c *Client) addOperationRequestSpotInstancesMiddlewares(stack *middleware.Stack, options Options) (err error) {
	err = stack.Serialize.Add(&awsEc2query_serializeOpRequestSpotInstances{}, middleware.After)
	if err != nil {
		return err
	}
	err = stack.Deserialize.Add(&awsEc2query_deserializeOpRequestSpotInstances{}, middleware.After)
	if err != nil {
		return err
	}
	if err = addlegacyEndpointContextSetter(stack, options); err != nil {
		return err
	}
	if err = addSetLoggerMiddleware(stack, options); err != nil {
		return err
	}
	if err = awsmiddleware.AddClientRequestIDMiddleware(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddComputeContentLengthMiddleware(stack); err != nil {
		return err
	}
	if err = addResolveEndpointMiddleware(stack, options); err != nil {
		return err
	}
	if err = v4.AddComputePayloadSHA256Middleware(stack); err != nil {
		return err
	}
	if err = addRetryMiddlewares(stack, options); err != nil {
		return err
	}
	if err = addHTTPSignerV4Middleware(stack, options); err != nil {
		return err
	}
	if err = awsmiddleware.AddRawResponseToMetadata(stack); err != nil {
		return err
	}
	if err = awsmiddleware.AddRecordResponseTiming(stack); err != nil {
		return err
	}
	if err = addClientUserAgent(stack, options); err != nil {
		return err
	}
	if err = smithyhttp.AddErrorCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = addRequestSpotInstancesResolveEndpointMiddleware(stack, options); err != nil {
		return err
	}
	if err = addOpRequestSpotInstancesValidationMiddleware(stack); err != nil {
		return err
	}
	if err = stack.Initialize.Add(newServiceMetadataMiddleware_opRequestSpotInstances(options.Region), middleware.Before); err != nil {
		return err
	}
	if err = awsmiddleware.AddRecursionDetection(stack); err != nil {
		return err
	}
	if err = addRequestIDRetrieverMiddleware(stack); err != nil {
		return err
	}
	if err = addResponseErrorMiddleware(stack); err != nil {
		return err
	}
	if err = addRequestResponseLogging(stack, options); err != nil {
		return err
	}
	if err = addendpointDisableHTTPSMiddleware(stack, options); err != nil {
		return err
	}
	return nil
}

func newServiceMetadataMiddleware_opRequestSpotInstances(region string) *awsmiddleware.RegisterServiceMetadata {
	return &awsmiddleware.RegisterServiceMetadata{
		Region:        region,
		ServiceID:     ServiceID,
		SigningName:   "ec2",
		OperationName: "RequestSpotInstances",
	}
}

type opRequestSpotInstancesResolveEndpointMiddleware struct {
	EndpointResolver EndpointResolverV2
	BuiltInResolver  builtInParameterResolver
}

func (*opRequestSpotInstancesResolveEndpointMiddleware) ID() string {
	return "ResolveEndpointV2"
}

func (m *opRequestSpotInstancesResolveEndpointMiddleware) HandleSerialize(ctx context.Context, in middleware.SerializeInput, next middleware.SerializeHandler) (
	out middleware.SerializeOutput, metadata middleware.Metadata, err error,
) {
	if awsmiddleware.GetRequiresLegacyEndpoints(ctx) {
		return next.HandleSerialize(ctx, in)
	}

	req, ok := in.Request.(*smithyhttp.Request)
	if !ok {
		return out, metadata, fmt.Errorf("unknown transport type %T", in.Request)
	}

	if m.EndpointResolver == nil {
		return out, metadata, fmt.Errorf("expected endpoint resolver to not be nil")
	}

	params := EndpointParameters{}

	m.BuiltInResolver.ResolveBuiltIns(&params)

	var resolvedEndpoint smithyendpoints.Endpoint
	resolvedEndpoint, err = m.EndpointResolver.ResolveEndpoint(ctx, params)
	if err != nil {
		return out, metadata, fmt.Errorf("failed to resolve service endpoint, %w", err)
	}

	req.URL = &resolvedEndpoint.URI

	for k := range resolvedEndpoint.Headers {
		req.Header.Set(
			k,
			resolvedEndpoint.Headers.Get(k),
		)
	}

	authSchemes, err := internalauth.GetAuthenticationSchemes(&resolvedEndpoint.Properties)
	if err != nil {
		var nfe *internalauth.NoAuthenticationSchemesFoundError
		if errors.As(err, &nfe) {
			// if no auth scheme is found, default to sigv4
			signingName := "ec2"
			signingRegion := m.BuiltInResolver.(*builtInResolver).Region
			ctx = awsmiddleware.SetSigningName(ctx, signingName)
			ctx = awsmiddleware.SetSigningRegion(ctx, signingRegion)

		}
		var ue *internalauth.UnSupportedAuthenticationSchemeSpecifiedError
		if errors.As(err, &ue) {
			return out, metadata, fmt.Errorf(
				"This operation requests signer version(s) %v but the client only supports %v",
				ue.UnsupportedSchemes,
				internalauth.SupportedSchemes,
			)
		}
	}

	for _, authScheme := range authSchemes {
		switch authScheme.(type) {
		case *internalauth.AuthenticationSchemeV4:
			v4Scheme, _ := authScheme.(*internalauth.AuthenticationSchemeV4)
			var signingName, signingRegion string
			if v4Scheme.SigningName == nil {
				signingName = "ec2"
			} else {
				signingName = *v4Scheme.SigningName
			}
			if v4Scheme.SigningRegion == nil {
				signingRegion = m.BuiltInResolver.(*builtInResolver).Region
			} else {
				signingRegion = *v4Scheme.SigningRegion
			}
			if v4Scheme.DisableDoubleEncoding != nil {
				// The signer sets an equivalent value at client initialization time.
				// Setting this context value will cause the signer to extract it
				// and override the value set at client initialization time.
				ctx = internalauth.SetDisableDoubleEncoding(ctx, *v4Scheme.DisableDoubleEncoding)
			}
			ctx = awsmiddleware.SetSigningName(ctx, signingName)
			ctx = awsmiddleware.SetSigningRegion(ctx, signingRegion)
			break
		case *internalauth.AuthenticationSchemeV4A:
			v4aScheme, _ := authScheme.(*internalauth.AuthenticationSchemeV4A)
			if v4aScheme.SigningName == nil {
				v4aScheme.SigningName = aws.String("ec2")
			}
			if v4aScheme.DisableDoubleEncoding != nil {
				// The signer sets an equivalent value at client initialization time.
				// Setting this context value will cause the signer to extract it
				// and override the value set at client initialization time.
				ctx = internalauth.SetDisableDoubleEncoding(ctx, *v4aScheme.DisableDoubleEncoding)
			}
			ctx = awsmiddleware.SetSigningName(ctx, *v4aScheme.SigningName)
			ctx = awsmiddleware.SetSigningRegion(ctx, v4aScheme.SigningRegionSet[0])
			break
		case *internalauth.AuthenticationSchemeNone:
			break
		}
	}

	return next.HandleSerialize(ctx, in)
}

func addRequestSpotInstancesResolveEndpointMiddleware(stack *middleware.Stack, options Options) error {
	return stack.Serialize.Insert(&opRequestSpotInstancesResolveEndpointMiddleware{
		EndpointResolver: options.EndpointResolverV2,
		BuiltInResolver: &builtInResolver{
			Region:       options.Region,
			UseDualStack: options.EndpointOptions.UseDualStackEndpoint,
			UseFIPS:      options.EndpointOptions.UseFIPSEndpoint,
			Endpoint:     options.BaseEndpoint,
		},
	}, "ResolveEndpoint", middleware.After)
}