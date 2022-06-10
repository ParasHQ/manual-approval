package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/go-github/v43/github"
)

type approvalEnvironment struct {
	client                  *github.Client
	repoFullName            string
	repo                    string
	repoOwner               string
	runID                   int
	approvers               []string
	minimumApprovals        int
	approvalIssue           *github.Issue
	approvalIssueNumber     int
	mutlipleDeploymentNames []string
}

func newApprovalEnvironment(client *github.Client, repoFullName, repoOwner string, runID int, approvers []string, minimumApprovals int, mutlipleDeploymentNames []string) (*approvalEnvironment, error) {
	repoOwnerAndName := strings.Split(repoFullName, "/")
	if len(repoOwnerAndName) != 2 {
		return nil, fmt.Errorf("repo owner and name in unexpected format: %s", repoFullName)
	}
	repo := repoOwnerAndName[1]

	return &approvalEnvironment{
		client:                  client,
		repoFullName:            repoFullName,
		repo:                    repo,
		repoOwner:               repoOwner,
		runID:                   runID,
		approvers:               approvers,
		minimumApprovals:        minimumApprovals,
		mutlipleDeploymentNames: mutlipleDeploymentNames,
	}, nil
}

func (a approvalEnvironment) runURL() string {
	return fmt.Sprintf("https://github.com/%s/actions/runs/%d", a.repoFullName, a.runID)
}

func (a *approvalEnvironment) createApprovalIssue(ctx context.Context) error {
	issueTitle := fmt.Sprintf("Manual approval required for workflow run %d", a.runID)
	issueMultipleDeployment := []string{"-"}
	if len(a.mutlipleDeploymentNames) > 0 {
		issueMultipleDeployment = a.mutlipleDeploymentNames
	}
	issueBody := fmt.Sprintf(`Workflow is pending manual review.
URL: %s

Required approvers: %s

Multiple deployment: %s

Respond %s to continue workflow or %s to cancel.`,
		a.runURL(),
		a.approvers,
		issueMultipleDeployment,
		formatAcceptedWords(approvedWords, a.mutlipleDeploymentNames),
		formatAcceptedWords(deniedWords, []string{}),
	)
	var err error
	fmt.Printf(
		"Creating issue in repo %s/%s with the following content:\nTitle: %s\nApprovers: %s\nBody:\n%s\n",
		a.repoOwner,
		a.repo,
		issueTitle,
		a.approvers,
		issueBody,
	)
	a.approvalIssue, _, err = a.client.Issues.Create(ctx, a.repoOwner, a.repo, &github.IssueRequest{
		Title:     &issueTitle,
		Body:      &issueBody,
		Assignees: &a.approvers,
	})
	a.approvalIssueNumber = a.approvalIssue.GetNumber()
	return err
}

func approvalFromComments(comments []*github.IssueComment, approvers []string, minimumApprovals int, multipleDeploymentNames []string) (approvalStatus approvalStatus, deploymentNames []string, error error) {
	remainingApprovers := make([]string, len(approvers))
	copy(remainingApprovers, approvers)

	if minimumApprovals == 0 {
		minimumApprovals = len(approvers)
	}

	for _, comment := range comments {
		commentUser := comment.User.GetLogin()
		approverIdx := approversIndex(remainingApprovers, commentUser)
		if approverIdx < 0 {
			continue
		}

		commentBody := comment.GetBody()

		var bodyDeploymentNames []string
		if strings.Contains(commentBody, "[") && len(multipleDeploymentNames) != 0 {
			commentBodySplit := strings.Split(commentBody, "[")
			commentBody = commentBodySplit[0]

			deploymentNamesRaw := "["
			deploymentNamesRaw += commentBodySplit[1]

			re := regexp.MustCompile(`\[(.*)\]`)
			matches := re.FindStringSubmatch(deploymentNamesRaw)
			if len(matches) != 2 {
				return approvalStatusPending, []string{},fmt.Errorf("errors.comment by not valid")
			}

			var validDeploymentNamesMap map[string]bool
			for _, v := range multipleDeploymentNames {
				validDeploymentNamesMap[v] = true
			}
			deploymentNames := strings.Split(matches[1], ",")
			for _, v := range deploymentNames {
				if !validDeploymentNamesMap[v] {
					return approvalStatusPending, []string{},fmt.Errorf("errors.deployment name is invalid")
				}
				bodyDeploymentNames = append(bodyDeploymentNames, v)
			}
		}

		isApprovalComment, err := isApproved(commentBody)
		if err != nil {
			return approvalStatusPending, []string{},  err
		}
		if isApprovalComment {
			if len(remainingApprovers) == len(approvers)-minimumApprovals+1 {
				return approvalStatusApproved, bodyDeploymentNames, nil
			}
			remainingApprovers[approverIdx] = remainingApprovers[len(remainingApprovers)-1]
			remainingApprovers = remainingApprovers[:len(remainingApprovers)-1]
			continue
		}

		isDenialComment, err := isDenied(commentBody)
		if err != nil {
			return approvalStatusPending, []string{}, err
		}
		if isDenialComment {
			return approvalStatusDenied, []string{}, nil
		}
	}

	return approvalStatusPending, []string{}, nil
}

func approversIndex(approvers []string, name string) int {
	for idx, approver := range approvers {
		if approver == name {
			return idx
		}
	}
	return -1
}

func isApproved(commentBody string) (bool, error) {
	for _, approvedWord := range approvedWords {
		matched, err := regexp.MatchString(fmt.Sprintf("(?i)^%s[.!]*\n*$", approvedWord), commentBody)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}

	return false, nil
}

func isDenied(commentBody string) (bool, error) {
	for _, deniedWord := range deniedWords {
		matched, err := regexp.MatchString(fmt.Sprintf("(?i)^%s[.!]?$", deniedWord), commentBody)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}

	return false, nil
}

func formatAcceptedWords(words []string, multipleDeploymentNames []string) string {
	var quotedWords []string

	var deploymentNames string
	if len(multipleDeploymentNames) > 1 {
		deploymentNames += "["
		for i, v := range multipleDeploymentNames {
			deploymentNames += v
			if i != len(multipleDeploymentNames)-1 {
				deploymentNames += ","
			}
		}
		deploymentNames += "]"
	}

	for _, word := range words {
		quotedWords = append(quotedWords, fmt.Sprintf("\"%s%s\"", word, deploymentNames))
	}

	return strings.Join(quotedWords, ", ")
}
