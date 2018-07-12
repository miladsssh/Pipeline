package main

import (
	"log"
	"fmt"
	"os"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/google/go-github/github"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codepipeline"
	"github.com/aws/aws-sdk-go/service/cloudformation"
)

const (
	PREFIX_CHANGE_SET = "CHANGESETFOR"
	PREFIX_STACK = "STACKFOR"
)

var codepipelineTemplate = os.Getenv("PIPELINE_NAME")

func pipelineExists(target string) bool {
	sess := session.Must(session.NewSession())

	svc := codepipeline.New(sess)

	resp, _ := svc.GetPipeline(&codepipeline.GetPipelineInput{
		Name: &target,
	})
	return resp.Pipeline != nil
}

func clonePipeline(source, target, branch string) error {
	sess := session.Must(session.NewSession())

	svc := codepipeline.New(sess)

	log.Printf("Creating new pipeline for branch: %s\n", branch)
	resp, err := svc.GetPipeline(&codepipeline.GetPipelineInput{
		Name: &source,
	})
	if err != nil {
		return err
	}

	for _, param := range resp.Pipeline.Stages {
		for _, action := range param.Actions {
			if *action.ActionTypeId.Category != "Deploy" {
				continue
			}
			*action.Configuration["ChangeSetName"] = PREFIX_CHANGE_SET + target
			*action.Configuration["StackName"] = PREFIX_STACK + target

			if *action.Configuration["ActionMode"] == "CHANGE_SET_REPLACE" {
				*action.Configuration["ParameterOverrides"] = "{\n\"PullRequest\": \""+target+"\"\n}"
				log.Printf("pipeline action deploy")
			}

			log.Printf("pipeline action: %s", action)
		}
	}


	pipeline := &codepipeline.PipelineDeclaration{
		Name:          &target,
		RoleArn:       resp.Pipeline.RoleArn,
		ArtifactStore: resp.Pipeline.ArtifactStore,
		Stages:        resp.Pipeline.Stages,
	}



	oauthToken := os.Getenv("GITHUB_OAUTH_TOKEN")

	pipeline.Stages[0].Actions[0].Configuration["OAuthToken"] = &oauthToken
	pipeline.Stages[0].Actions[0].Configuration["Branch"] = &branch

	_, err = svc.CreatePipeline(&codepipeline.CreatePipelineInput{
		Pipeline: pipeline,
	})
	return err
}

func destroyPipeline(target string) error {
	sess := session.Must(session.NewSession())

	svc := codepipeline.New(sess)

	cf := cloudformation.New(sess)

	stackName := PREFIX_STACK + target

	_, err:= cf.DeleteStack(&cloudformation.DeleteStackInput{
		StackName: &stackName,
	})

	if err != nil {
		return err
	}

	_, err = svc.DeletePipeline(&codepipeline.DeletePipelineInput{
		Name: &target,
	})

	return err
}

func Handler(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {


	log.Printf("Processing Lambda request %s\n", request.RequestContext.RequestID)
	log.Printf("Event Name %s\n", request.Headers["X-GitHub-Event"])

	if request.Headers["X-GitHub-Event"] == "pull_request" {
		prEvt := new(github.PullRequestEvent)
		json.Unmarshal([]byte(request.Body), prEvt)

		prName := fmt.Sprintf("%s%d", os.Getenv("PREFIX_NAME"), *prEvt.PullRequest.Number)

		log.Printf("Pull Request Title %s\n", *prEvt.PullRequest.Title)
		log.Printf("Pull Request Name %s\n", prName)
		log.Printf("Pull Request state %s\n", *prEvt.PullRequest.State)

		if *prEvt.PullRequest.State == "open" {

			log.Println("Pull Request is opened")

			if !pipelineExists(prName) {
				log.Println("Pipeline don't exists")
				err := clonePipeline(codepipelineTemplate, prName, *prEvt.PullRequest.Head.Ref)
				if v, ok := err.(awserr.Error); ok {
					log.Printf("failed: %#v %#v\n", v.Message(), v.OrigErr())
				}
			}else{
				log.Println("Pipeline exists")
			}


		} else if *prEvt.PullRequest.State == "closed" {
			if pipelineExists(prName) {
				err := destroyPipeline(prName)
				if err != nil {
					log.Printf("failed: %#v", err)
				}
			}
		}
	}

	return events.APIGatewayProxyResponse{Body: request.Body, StatusCode: 200}, nil

}

func main() {
	lambda.Start(Handler)
}