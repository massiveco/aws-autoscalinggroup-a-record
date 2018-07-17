export AWS_REGION=us-east-1
export GOCACHE=off

test:
	go test ./...

build:
	GOOS=linux go build .
	zip deploy.zip aws-autoscalinggroup-a-record
	rm aws-autoscalinggroup-a-record

release: build
	aws s3 cp ./deploy.zip s3://ma.ssive.co/lambdas/massive_autoscaling_a_record.zip
