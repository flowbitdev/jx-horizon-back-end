package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type R2Client struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	PublicURL       string
	HTTPClient      *http.Client
}

func NewR2ClientFromEnv() *R2Client {
	accountID := os.Getenv("R2_ACCOUNT_ID")
	accessKey := os.Getenv("R2_ACCESS_KEY_ID")
	secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")
	bucketName := os.Getenv("R2_BUCKET_NAME")
	publicURL := os.Getenv("R2_PUBLIC_URL")

	if bucketName == "" {
		bucketName = "jxhorizon"
	}
	if publicURL == "" && accountID != "" {
		publicURL = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	}

	return &R2Client{
		AccountID:       accountID,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		BucketName:      bucketName,
		PublicURL:       publicURL,
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *R2Client) IsConfigured() bool {
	return r.AccountID != "" && r.AccessKeyID != "" && r.SecretAccessKey != ""
}

func (r *R2Client) Upload(ctx context.Context, objectKey string, data []byte, contentType string) (string, error) {
	if !r.IsConfigured() {
		return "", fmt.Errorf("R2 credentials not fully configured")
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	region := "auto"
	service := "s3"
	host := fmt.Sprintf("%s.r2.cloudflarestorage.com", r.AccountID)

	canonicalURI := fmt.Sprintf("/%s/%s", r.BucketName, objectKey)
	endpoint := fmt.Sprintf("https://%s%s", host, canonicalURI)

	hasher := sha256.New()
	hasher.Write(data)
	payloadHash := hex.EncodeToString(hasher.Sum(nil))

	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", contentType, host, payloadHash, amzDate)
	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalQuery := ""

	canonicalRequest := fmt.Sprintf("PUT\n%s\n%s\n%s\n%s\npayloadHash", canonicalURI, canonicalQuery, canonicalHeaders, signedHeaders)
	canonicalRequest = strings.Replace(canonicalRequest, "payloadHash", payloadHash, 1)

	reqHasher := sha256.New()
	reqHasher.Write([]byte(canonicalRequest))
	canonicalReqHash := hex.EncodeToString(reqHasher.Sum(nil))

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s", algorithm, amzDate, credentialScope, canonicalReqHash)

	signingKey := getSignatureKey(r.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSign(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s", algorithm, r.AccessKeyID, credentialScope, signedHeaders, signature)

	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create R2 HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", authHeader)

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("R2 upload HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		log.Error().Int("status", resp.StatusCode).Str("body", string(respBody)).Msg("R2 upload failed")
		return "", fmt.Errorf("R2 upload failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	publicURL := r.PublicURL
	if !strings.HasSuffix(publicURL, "/") {
		publicURL += "/"
	}
	fileURL := fmt.Sprintf("%s%s/%s", publicURL, r.BucketName, objectKey)

	log.Info().Str("objectKey", objectKey).Str("fileURL", fileURL).Msg("Successfully uploaded file to Cloudflare R2")
	return fileURL, nil
}

func hmacSign(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func getSignatureKey(key, dateStamp, regionName, serviceName string) []byte {
	kDate := hmacSign([]byte("AWS4"+key), []byte(dateStamp))
	kRegion := hmacSign(kDate, []byte(regionName))
	kService := hmacSign(kRegion, []byte(serviceName))
	kSigning := hmacSign(kService, []byte("aws4_request"))
	return kSigning
}
