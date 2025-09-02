## USAGE:

- Build the image:

` podman build -t cluster-status-updater:test .`

- Then run it with the following command:

```
podman run --rm -it \
  -e "API_ROUTE=https://restricted-api-route.com" \
  -e "ADMIN_TOKEN=youradmintoken" \
  -e "SPREADSHEET_ID=googlesheetid" \
  -e "GOOGLE_APPLICATION_CREDENTIALS=/app/credentials.json" \
  -v "$(pwd)/credentials.json:/app/credentials.json:ro" \
  cluster-status-updater:test
```

Where 
- `API_ROUTE` is the new endpoint available on the infra cluster that exposes the information https://github.com/rhpds/sandbox/pull/154
- `ADMIN_TOKEN` is a base64 encoded JWT token for openshift
- `SPREADSHEET_ID` is a google sheet ID ([You can take it from the url](https://docs.meiro.io/books/meiro-integrations/page/where-can-i-find-the-sheet-id-of-google-spreadsheet-file))
`- GOOGLE_APPLICATION_CREDENTIALS` is a json service account key file ([Create a workspace SA in the Google Cloud Console](https://developers.google.com/workspace/guides/create-credentials) and add the email of the SA as editor in the sheet)
