package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
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
		Clusters map[string]interface{} `json:"clusters"`
	} `json:"body"`
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

	// 4. Dynamically flatten and prepare data for Google Sheets
	log.Println("Flattening data and generating dynamic header...")

	headerMap := make(map[string]bool)
	var flattenedClusters []map[string]string

	for clusterName, clusterInterface := range clusterData.Body.Clusters {
		flattenedData := flatten(clusterInterface, "")

		// Add the cluster name as a field
		flattenedData["cluster_name"] = clusterName

		for key := range flattenedData {
			headerMap[key] = true
		}
		flattenedClusters = append(flattenedClusters, flattenedData)
	}

	var header []string
	for key := range headerMap {
		header = append(header, key)
	}
	sort.Strings(header)

	headerRow := make([]interface{}, len(header))
	for i, v := range header {
		headerRow[i] = v
	}

	rows := [][]interface{}{headerRow}

	for _, clusterData := range flattenedClusters {
		var row []interface{}
		for _, key := range header {
			value, ok := clusterData[key]
			if !ok {
				row = append(row, "")
			} else {
				row = append(row, value)
			}
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

	_, err = srv.Spreadsheets.Values.Clear(spreadsheetID, "full_data!A1:Z", &sheets.ClearValuesRequest{}).Do()
	if err != nil {
		log.Fatalf("Failed to clear sheet: %v", err)
	}

	_, err = srv.Spreadsheets.Values.Update(spreadsheetID, "full_data!A1", valueRange).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		log.Fatalf("Failed to update sheet: %v", err)
	}

	log.Println("Successfully updated Google Sheet!")
}

// flatten recursively flattens a nested map and collects all key-value pairs.
func flatten(jsonMap interface{}, prefix string) map[string]string {
	flattened := make(map[string]string)

	switch m := jsonMap.(type) {
	case map[string]interface{}:
		for key, value := range m {
			newPrefix := key
			if prefix != "" {
				newPrefix = prefix + "_" + key
			}
			switch nestedValue := value.(type) {
			case map[string]interface{}:
				nestedFlattened := flatten(nestedValue, newPrefix)
				for k, v := range nestedFlattened {
					flattened[k] = v
				}
			case []interface{}:
				if len(nestedValue) > 0 {
					nestedFlattened := flatten(nestedValue[0], newPrefix)
					for k, v := range nestedFlattened {
						flattened[k] = v
					}
				}
			case string:
				flattened[newPrefix] = nestedValue
			case float64:
				flattened[newPrefix] = fmt.Sprintf("%v", nestedValue)
			case bool:
				flattened[newPrefix] = fmt.Sprintf("%t", nestedValue)
			default:
				flattened[newPrefix] = fmt.Sprintf("%v", nestedValue)
			}
		}
	}
	return flattened
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
