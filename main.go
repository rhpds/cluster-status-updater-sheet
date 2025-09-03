package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Main structs to map the JSON response
type LoginResponse struct {
	AccessToken string `json:"access_token"`
}

type StatusResponse struct {
	Status string `json:"status"`
	Body   struct {
		Clusters map[string]Cluster `json:"clusters"`
	} `json:"body"`
}

type Cluster struct {
	ClusterName    string                    `json:"cluster_name"`
	OCPVersion     string                    `json:"ocp_version"`
	NodeSummary    NodeSummary               `json:"node_summary"`
	OperatorStatus map[string]OperatorStatus `json:"operator_status"`
	Configuration  struct {
		APIURL      string `json:"api_url"`
		Annotations struct {
			Cloud string `json:"cloud"`
		} `json:"annotations"`
	} `json:"configuration"`
}

type NodeSummary struct {
	Master int `json:"master"`
	Worker int `json:"worker"`
	Ready  int `json:"ready"`
	Total  int `json:"total"`
}

type OperatorStatus struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func main() {
	// 1. Load environment variables
	apiRoute := os.Getenv("API_ROUTE")
	adminToken := os.Getenv("ADMIN_TOKEN")
	spreadsheetID := os.Getenv("SPREADSHEET_ID")
	credsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")

	if apiRoute == "" || adminToken == "" || spreadsheetID == "" || credsFile == "" {
		log.Fatal("Environment variables API_ROUTE, ADMIN_TOKEN, SPREADSHEET_ID, and GOOGLE_APPLICATION_CREDENTIALS must be set")
	}

	// 2. Authenticate with the restricted endpoint
	log.Println("Authenticating with API...")
	token, err := getAccessToken(apiRoute, adminToken)
	if err != nil {
		log.Fatalf("Failed to get access token: %v", err)
	}

	// 3. Query the endpoint and poll for completion
	log.Println("Initiating status request...")
	clusterData, err := pollForStatus(apiRoute, token)
	if err != nil {
		log.Fatalf("Failed to retrieve cluster status: %v", err)
	}

	// 4. Transform data for Google Sheets
	log.Println("Parsing and preparing data for Google Sheets...")
	rows := [][]interface{}{
		{"Cluster Name", "OpenShift Version", "Cloud", "API url", "Ready Nodes", "Total Nodes", "Ingress Operator Status", "Operator Status"}, // Header
	}

	for _, cluster := range clusterData.Body.Clusters {
		ingressStatus := "N/A"
		if op, ok := cluster.OperatorStatus["ingress"]; ok {
			ingressStatus = op.Status
		}

		var overallOperatorStatus string
		healthyOperators := 0
		for _, status := range cluster.OperatorStatus {
			if status.Status == "Healthy" {
				healthyOperators++
			}
		}
		if healthyOperators == len(cluster.OperatorStatus) {
			overallOperatorStatus = "Healthy"
		} else {
			overallOperatorStatus = fmt.Sprintf("Unhealthy (%d/%d)", healthyOperators, len(cluster.OperatorStatus))
		}

		row := []interface{}{
			cluster.ClusterName,
			cluster.OCPVersion,
			cluster.Configuration.Annotations.Cloud,
			cluster.Configuration.APIURL,
			fmt.Sprintf("%d", cluster.NodeSummary.Ready),
			fmt.Sprintf("%d", cluster.NodeSummary.Total),
			ingressStatus,
			overallOperatorStatus,
		}
		rows = append(rows, row)
	}

	// 5. Update the Google Sheet
	log.Println("Updating Google Sheet...")
	ctx := context.Background()

	// Set up Google Sheets client
	b, err := os.ReadFile(credsFile)
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}
	config, err := google.JWTConfigFromJSON(b, sheets.SpreadsheetsScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := config.Client(ctx)
	srv, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	// Write data to sheet
	valueRange := &sheets.ValueRange{
		Values: rows,
	}

	_, err = srv.Spreadsheets.Values.Clear(spreadsheetID, "A1:Z", &sheets.ClearValuesRequest{}).Do()
	if err != nil {
		log.Fatalf("Failed to clear sheet: %v", err)
	}

	_, err = srv.Spreadsheets.Values.Update(spreadsheetID, "A1", valueRange).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		log.Fatalf("Failed to update sheet: %v", err)
	}

	log.Println("Successfully updated Google Sheet!")
}

func getAccessToken(apiRoute, adminToken string) (string, error) {
	req, err := http.NewRequest("GET", apiRoute+"/api/v1/login", nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", "Bearer "+adminToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed with status: %s", resp.Status)
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", err
	}
	return loginResp.AccessToken, nil
}

func pollForStatus(apiRoute, token string) (*StatusResponse, error) {
	// First, initiate the status request with a POST
	initialReq, err := http.NewRequest("POST", apiRoute+"/api/v1/ocp-shared-clusters/status", nil)
	if err != nil {
		return nil, err
	}
	initialReq.Header.Add("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(initialReq)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	// Then, poll the status with GET
	for i := 0; i < 10; i++ {
		log.Printf("Polling for status (attempt %d/10)...", i+1)
		time.Sleep(2 * time.Second) // Wait between polls

		statusReq, err := http.NewRequest("GET", apiRoute+"/api/v1/ocp-shared-clusters/status", nil)
		if err != nil {
			return nil, err
		}
		statusReq.Header.Add("Authorization", "Bearer "+token)

		statusResp, err := client.Do(statusReq)
		if err != nil {
			return nil, err
		}
		defer statusResp.Body.Close()

		var data StatusResponse
		if err := json.NewDecoder(statusResp.Body).Decode(&data); err != nil {
			return nil, err
		}

		if data.Status == "success" {
			return &data, nil
		}
	}

	return nil, fmt.Errorf("polling timed out, status never reached 'success'")
}
