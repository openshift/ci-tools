package jobrunaggregatorlib

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"github.com/spf13/pflag"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

type GoogleAuthenticationFlags struct {
	TokenFileLocation string
	// location of a credential file described by https://cloud.google.com/docs/authentication/production
	GoogleServiceAccountCredentialFile string
	GoogleOAuthClientCredentialFile    string
}

func NewGoogleAuthenticationFlags() *GoogleAuthenticationFlags {
	tokenDir := os.Getenv("HOME")
	if len(tokenDir) == 0 {
		tokenDir = "./"
	}
	return &GoogleAuthenticationFlags{
		TokenFileLocation: filepath.Join(tokenDir, "gcp-token.json"),
	}
}

func (f *GoogleAuthenticationFlags) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.GoogleServiceAccountCredentialFile, "google-service-account-credential-file", f.GoogleServiceAccountCredentialFile, "location of a credential file described by https://cloud.google.com/docs/authentication/production")
	fs.StringVar(&f.GoogleOAuthClientCredentialFile, "google-oauth-credential-file", f.GoogleOAuthClientCredentialFile, "location of a credential file described by https://developers.google.com/people/quickstart/go, setup from https://cloud.google.com/bigquery/docs/authentication/end-user-installed#client-credentials")
}

func (f *GoogleAuthenticationFlags) Validate() error {
	if len(f.GoogleServiceAccountCredentialFile) == 0 && len(f.GoogleOAuthClientCredentialFile) == 0 {
		return fmt.Errorf("one of --google-service-account-credential-file or --google-oauth-credential-file must be specified")
	}

	return nil
}

func (f *GoogleAuthenticationFlags) NewBigQueryClient(ctx context.Context, projectID string) (*bigquery.Client, error) {
	if len(f.GoogleServiceAccountCredentialFile) > 0 {
		return bigquery.NewClient(ctx,
			projectID,
			option.WithCredentialsFile(f.GoogleServiceAccountCredentialFile),
		)
	}

	b, err := os.ReadFile(f.GoogleOAuthClientCredentialFile)
	if err != nil {
		return nil, err
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/bigquery")
	if err != nil {
		return nil, err
	}
	token := f.getToken(config)

	return bigquery.NewClient(ctx,
		projectID,
		option.WithTokenSource(oauth2.StaticTokenSource(token)),
	)
}

func (f *GoogleAuthenticationFlags) NewGCSClient(ctx context.Context) (*storage.Client, error) {
	if len(f.GoogleServiceAccountCredentialFile) > 0 {
		return storage.NewClient(ctx,
			option.WithCredentialsFile(f.GoogleServiceAccountCredentialFile),
		)
	}

	b, err := os.ReadFile(f.GoogleOAuthClientCredentialFile)
	if err != nil {
		return nil, err
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/bigquery")
	if err != nil {
		return nil, err
	}
	token := f.getToken(config)

	return storage.NewClient(ctx,
		option.WithTokenSource(oauth2.StaticTokenSource(token)),
	)
}

func (f *GoogleAuthenticationFlags) NewCIGCSClient(ctx context.Context, gcsBucketName string) (CIGCSClient, error) {
	gcsClient, err := f.NewGCSClient(ctx)
	if err != nil {
		return nil, err
	}

	return &ciGCSClient{
		gcsClient:     gcsClient,
		gcsBucketName: gcsBucketName,
	}, nil
}

// Retrieve a token, saves the token, then returns the generated client.
func (f *GoogleAuthenticationFlags) getToken(config *oauth2.Config) *oauth2.Token {
	// The file token.json stores the user's access and refresh tokens, and is
	// created automatically when the authorization flow completes for the first
	// time.
	tokFile := f.TokenFileLocation
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return tok
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(token); err != nil {
		fmt.Println(err.Error())
	}
}
