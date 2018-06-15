package reactor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/massiveco/aws-hostname/identity"
)

// Reactor Manage ASG Lifecycle for individual nodes
type Reactor struct {
	route53Client     *route53.Route53
	autoscalingClient *autoscaling.AutoScaling
	ec2Client         *ec2.EC2
	ec2Metadata       *ec2metadata.EC2Metadata
}

type autoscalingEvent struct {
	EC2InstanceID        string `json:"EC2InstanceId"`
	AutoScalingGroupName string
	Event                string
}

// New Create a new reactor to ASG SNS Events
func New(sess *session.Session) Reactor {

	if sess == nil {
		sess, _ = session.NewSessionWithOptions(session.Options{
			Config: aws.Config{
				HTTPClient: http.DefaultClient,
			},
			SharedConfigState: session.SharedConfigEnable,
		})
	}

	return Reactor{
		route53Client:     route53.New(sess),
		autoscalingClient: autoscaling.New(sess),
		ec2Client:         ec2.New(sess),
	}
}

func (r Reactor) processEvent(event autoscalingEvent) (*string, error) {
	asg, err := r.lookupAutoScalingGroup(event.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	instance, err := r.getInstance(event.EC2InstanceID)
	if err != nil {
		return nil, err
	}

	zoneID := extractTag("massive:DNS-SD:Route53:zone", asg.Tags)
	zone, err := r.route53Client.GetHostedZone(&route53.GetHostedZoneInput{Id: zoneID})
	if err != nil {
		return nil, err
	}

	hostname, _ := identity.GenerateHostname(*instance)
	fqdn := strings.Join([]string{*hostname, *zone.HostedZone.Name}, ".")

	change := route53.Change{
		Action: aws.String("UPSERT"),
		ResourceRecordSet: &route53.ResourceRecordSet{
			Name:            aws.String(fqdn),
			Type:            aws.String("A"),
			ResourceRecords: []*route53.ResourceRecord{&route53.ResourceRecord{Value: aws.String(*instance.PrivateIpAddress)}},
			TTL:             aws.Int64(60),
		},
	}

	if event.Event != "autoscaling:EC2_INSTANCE_LAUNCH" {
		change.SetAction("DELETE")
	}

	_, err = r.route53Client.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{&change},
		},
	})
	if err != nil {
		return nil, err
	}

	return &event.EC2InstanceID, nil
}

func (r Reactor) getInstance(InstanceID string) (*ec2.Instance, error) {
	describedInstances, err := r.ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("instance-id"),
				Values: []*string{
					aws.String(InstanceID),
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if describedInstances.Reservations != nil && describedInstances.Reservations[0] != nil && describedInstances.Reservations[0].Instances[0] != nil {
		return describedInstances.Reservations[0].Instances[0], nil
	}
	return nil, nil
}

func (r Reactor) lookupAutoScalingGroup(name string) (*autoscaling.Group, error) {
	output, err := r.autoscalingClient.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{AutoScalingGroupNames: []*string{aws.String(name)}})
	if err != nil {
		return nil, err
	}
	if len(output.AutoScalingGroups) == 0 {
		return nil, errors.New("AutoScalingGroup not found")
	}

	return output.AutoScalingGroups[0], nil
}

//Handle a request
func (r Reactor) Handle(req events.SNSEvent) (*string, error) {
	if len(req.Records) == 0 || req.Records[0].SNS.Message == "" {
		return nil, errors.New("No SNS Message found")
	}

	message := req.Records[0].SNS.Message
	var evt autoscalingEvent
	err := json.Unmarshal([]byte(message), &evt)
	if err != nil {
		return nil, err
	}
	return r.processEvent(evt)
}

func extractTag(tagName string, tags []*autoscaling.TagDescription) *string {

	for _, tag := range tags {
		if *tag.Key == tagName {
			return tag.Value
		}
	}

	return nil
}

func extractTagFromInstance(tagName string, tags []*ec2.Tag) *string {

	for _, tag := range tags {
		if *tag.Key == tagName {
			return tag.Value
		}
	}

	return nil
}
