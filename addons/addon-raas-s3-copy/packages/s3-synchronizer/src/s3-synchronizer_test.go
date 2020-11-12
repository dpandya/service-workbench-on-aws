package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"io/ioutil"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testAwsSession *session.Session

const testRegion = "us-east-1"
const debug = true

// A test destination directory path. The test creates this directory and populates it with simulated downloads
// This directory is cleaned up at the end of the test.
// WARNING: Since this directory gets automatically cleaned up at the end of the test,
// make sure to not specify some higher path here as the test program will end up deleting the directory
// you specify here.
const destinationBase = "../.build/temp-output"

const testFakeBucketName = "test-bucket"

const testFileContentTemplate = "test file content for file = %d"
const testFileUpdatedContentTemplate = "UPDATED -- test file content for file = %d"

// ------------------------------- Test Cases -------------------------------/

// ######### Tests for Initial Downloads #########

// Test for single S3Mount
func TestMainImplForInitialDownloadSingleMount(t *testing.T) {

	// ---- Data setup ----
	noOfMounts := 1
	testMounts := make([]s3Mount, noOfMounts)
	testMountId := "TestMainImplForInitialDownloadSingleMount"
	noOfFilesInMount := 5
	testMounts[0] = *putReadOnlyTestMountFiles(t, testFakeBucketName, testMountId, noOfFilesInMount)
	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 2

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err = mainImpl(testAwsSession, debug, false, -1, 60, -1, concurrency, testMountsJson, destinationBase)
	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
		t.Errorf("Error: %v", err)
	}

	// ---- Assertions ----
	assertFilesDownloaded(t, testMountId, noOfFilesInMount)
}

// Test for multiple S3Mounts
func TestMainImplForInitialDownloadMultipleMounts(t *testing.T) {
	// ---- Data setup ----
	noOfMounts := 3
	testMounts := make([]s3Mount, noOfMounts)
	testBucketName := "test-bucket"

	testMountId1 := "TestMainImplForInitialDownloadMultipleMounts1"
	noOfFilesInMount1 := 5
	testMounts[0] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId1, noOfFilesInMount1)

	testMountId2 := "TestMainImplForInitialDownloadMultipleMounts2"
	noOfFilesInMount2 := 1
	testMounts[1] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId2, noOfFilesInMount2)

	testMountId3 := "TestMainImplForInitialDownloadMultipleMounts3"
	noOfFilesInMount3 := 0 // Test mount containing no files (simulating empty folder in S3)
	testMounts[2] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId3, noOfFilesInMount3)

	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 2

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err = mainImpl(testAwsSession, debug, false, -1, 60, -1, concurrency, testMountsJson, destinationBase)
	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
		t.Errorf("Error: %v", err)
	}

	// ---- Assertions ----
	assertFilesDownloaded(t, testMountId1, noOfFilesInMount1)
	assertFilesDownloaded(t, testMountId2, noOfFilesInMount2)
	assertFilesDownloaded(t, testMountId3, noOfFilesInMount3)
}

// Test for s3Mounts json being empty array
func TestMainImplForInitialDownloadEmptyMounts(t *testing.T) {
	// ---- Data setup ----
	var testMounts []s3Mount

	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 2

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err = mainImpl(testAwsSession, debug, false, -1, 60, -1, concurrency, testMountsJson, destinationBase)
	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
		t.Errorf("Error: %v", err)
	}
}

// Negative test: Test for invalid s3Mounts json
func TestMainImplForInitialDownloadInvalidMounts(t *testing.T) {
	// ---- Inputs ----
	concurrency := 2

	testMountsJson := "some invalid json"
	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err := mainImpl(testAwsSession, debug, false, -1, 60, -1, concurrency, testMountsJson, destinationBase)
	if err == nil {
		// Fail test in case of no errors since we are expecting errors when passing invalid json for mounting
		t.Logf("Expecting error when running the main s3-synchronizer with invalid testMountsJson but it ran fine")
	}
}

// ######### Tests for Recurring Downloads #########

// Test for single S3Mount with recurring downloads
// - Make sure S3 ADDs are synced automatically
// - Make sure S3 UPDATEs are synced automatically
// - Make sure S3 DELETEs are synced automatically
func TestMainImplForSyncSingleMount(t *testing.T) {
	// ---- Data setup ----
	noOfMounts := 1
	testMounts := make([]s3Mount, noOfMounts)
	testMountId := "TestMainImplForSyncSingleMount"
	noOfFilesInMount := 5
	testMounts[0] = *putReadOnlyTestMountFiles(t, testFakeBucketName, testMountId, noOfFilesInMount)
	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 5
	recurringDownloads := true
	stopRecurringDownloadsAfter := 5
	downloadInterval := 1

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	var wg sync.WaitGroup

	// Trigger recurring download in a separate thread and increment the wait group counter
	wg.Add(1)
	go func() {
		// ---- Run code under test ----
		err = mainImpl(testAwsSession, debug, recurringDownloads, stopRecurringDownloadsAfter, downloadInterval, -1, concurrency, testMountsJson, destinationBase)
		if err != nil {
			// Fail test in case of any errors
			t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
			t.Errorf("Error: %v", err)
		}

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	// In a separate thread add few more files to the mount point and verify that they get downloaded
	// by the recurring downloader thread after the dow
	wg.Add(1)
	go func() {
		// TEST FOR ADD -- NEW UPLOAD TO S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		// Upload same number of files in the mount again (i.e., double the noOfFilesInMount)
		testMounts[0] = *putReadOnlyTestMountFiles(t, testFakeBucketName, testMountId, 2*noOfFilesInMount)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the newly uploaded files are automatically downloaded after the download interval
		assertFilesDownloaded(t, testMountId, 2*noOfFilesInMount)

		// TEST FOR UPDATE -- UPLOAD TO EXISTING FILES IN S3 --> LOCAL FILE SYSTEM SYNC
		// -----------------------------------------------------------------------------

		// Update the files in S3
		updateTestMountFiles(t, testFakeBucketName, testMountId, noOfFilesInMount)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the updated files are automatically downloaded after the download interval
		assertUpdatedFilesDownloaded(t, testMountId, noOfFilesInMount)

		// TEST FOR DELETE -- DELETE FROM S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		fileIdxToDelete := noOfFilesInMount + 1
		// Delete some files from S3 and make sure they automatically get deleted from local file system
		deleteTestMountFile(t, testFakeBucketName, testMountId, fileIdxToDelete)

		// Sleep for the download interval duration plus some more buffer time to allow sync to happen
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the file deleted from S3 are automatically deleted after the download interval
		assertFileDeleted(t, testMountId, fileIdxToDelete)

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	wg.Wait() // Wait until all spawned go routines complete before existing the test case
}

// Test for multiple S3Mounts with recurring downloads
// - Make sure S3 ADDs are synced automatically
// - Make sure S3 UPDATEs are synced automatically
// - Make sure S3 DELETEs are synced automatically
func TestMainImplForSyncMultipleMounts(t *testing.T) {
	// ---- Data setup ----
	noOfMounts := 3
	testMounts := make([]s3Mount, noOfMounts)
	testBucketName := "test-bucket"

	testMountId1 := "TestMainImplForSyncMultipleMounts1"
	noOfFilesInMount1 := 5
	testMounts[0] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId1, noOfFilesInMount1)

	testMountId2 := "TestMainImplForSyncMultipleMounts2"
	noOfFilesInMount2 := 1
	testMounts[1] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId2, noOfFilesInMount2)

	testMountId3 := "TestMainImplForSyncMultipleMounts3"
	noOfFilesInMount3 := 0 // Test mount containing no files (simulating empty folder in S3)
	testMounts[2] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId3, noOfFilesInMount3)

	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 5
	recurringDownloads := true
	stopRecurringDownloadsAfter := 5
	downloadInterval := 1

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	var wg sync.WaitGroup
	// Trigger recurring download in a separate thread and increment the wait group counter
	wg.Add(1)
	go func() {
		// ---- Run code under test ----
		err = mainImpl(testAwsSession, debug, recurringDownloads, stopRecurringDownloadsAfter, downloadInterval, -1, concurrency, testMountsJson, destinationBase)
		if err != nil {
			// Fail test in case of any errors
			t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
			t.Errorf("Error: %v", err)
		}

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	// In a separate thread add few more files to the mount point and verify that they get downloaded
	// by the recurring downloader thread after the dow
	wg.Add(1)
	go func() {
		// TEST FOR ADD -- NEW UPLOAD TO S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		// Upload same number of files in the mount again (i.e., double the noOfFilesInMount)
		testMounts[0] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId1, 2*noOfFilesInMount1)
		testMounts[1] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId2, 2*noOfFilesInMount2)
		testMounts[2] = *putReadOnlyTestMountFiles(t, testBucketName, testMountId3, 2*noOfFilesInMount3)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the newly uploaded files are automatically downloaded after the download interval
		assertFilesDownloaded(t, testMountId1, 2*noOfFilesInMount1)
		assertFilesDownloaded(t, testMountId2, 2*noOfFilesInMount2)
		assertFilesDownloaded(t, testMountId3, 2*noOfFilesInMount3)

		// TEST FOR UPDATE -- UPLOAD TO EXISTING FILES IN S3 --> LOCAL FILE SYSTEM SYNC
		// -----------------------------------------------------------------------------

		// Update the files in S3
		updateTestMountFiles(t, testFakeBucketName, testMountId1, noOfFilesInMount1)
		updateTestMountFiles(t, testFakeBucketName, testMountId2, noOfFilesInMount2)
		updateTestMountFiles(t, testFakeBucketName, testMountId3, noOfFilesInMount3)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the updated files are automatically downloaded after the download interval
		assertUpdatedFilesDownloaded(t, testMountId1, noOfFilesInMount1)
		assertUpdatedFilesDownloaded(t, testMountId2, noOfFilesInMount2)
		assertUpdatedFilesDownloaded(t, testMountId3, noOfFilesInMount3)

		// TEST FOR DELETE -- DELETE FROM S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		fileIdxToDelete1 := noOfFilesInMount1 + 1
		fileIdxToDelete2 := noOfFilesInMount2 + 1
		// Delete some files from S3 and make sure they automatically get deleted from local file system
		deleteTestMountFile(t, testFakeBucketName, testMountId1, fileIdxToDelete1)
		deleteTestMountFile(t, testFakeBucketName, testMountId2, fileIdxToDelete2)

		// Sleep for the download interval duration plus some more buffer time to allow sync to happen
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the file deleted from S3 are automatically deleted after the download interval
		assertFileDeleted(t, testMountId1, fileIdxToDelete1)
		assertFileDeleted(t, testMountId2, fileIdxToDelete2)

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	wg.Wait() // Wait until all spawned go routines complete before existing the test case
}

// Test for s3Mounts json being empty array for recurring downloads
func TestMainImplForRecurringDownloadEmptyMounts(t *testing.T) {
	// ---- Data setup ----
	var testMounts []s3Mount

	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 2

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err = mainImpl(testAwsSession, debug, true, 5, 1, -1, concurrency, testMountsJson, destinationBase)
	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
		t.Errorf("Error: %v", err)
	}
}

// Negative test: Test for invalid s3Mounts json for recurring downloads
func TestMainImplForRecurringDownloadInvalidMounts(t *testing.T) {
	// ---- Inputs ----
	concurrency := 2

	testMountsJson := "some invalid json"
	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	// ---- Run code under test ----
	err := mainImpl(testAwsSession, debug, true, 5, 1, -1, concurrency, testMountsJson, destinationBase)
	if err == nil {
		// Fail test in case of no errors since we are expecting errors when passing invalid json for mounting
		t.Logf("Expecting error when running the main s3-synchronizer with invalid testMountsJson but it ran fine")
	}
}

// ######### Tests for Bi-directional Sync #########

//Test for single writeable S3Mount with recurring downloads (i.e., bi-directional sync)
//- Make sure S3 --> Local sync works correctly
//	 - Make sure S3 ADDs are synced to local automatically
//	 - Make sure S3 UPDATEs are synced to local automatically
//	 - Make sure S3 DELETEs are synced to local automatically
//
//- Make sure Local --> S3 sync works correctly
//  - Make sure local ADDs are synced to S3 automatically
//	 - Make sure local UPDATEs are synced to S3 automatically
//	 - Make sure local DELETEs are synced to S3 automatically
//	 - Make sure local RENAMEs are synced to S3 automatically
func TestMainImplForBiDirectionalSyncSingleMount(t *testing.T) {
	// ---- Data setup ----
	noOfMounts := 1
	testMounts := make([]s3Mount, noOfMounts)
	testMountId := "TestMainImplForBiDirectionalSyncSingleMount"
	noOfFilesInMount := 5
	testMounts[0] = *putWriteableTestMountFiles(t, testFakeBucketName, testMountId, noOfFilesInMount)
	testMountsJsonBytes, err := json.Marshal(testMounts)
	testMountsJson := string(testMountsJsonBytes)

	if err != nil {
		// Fail test in case of any errors
		t.Logf("Error creating test mount setup data %s", err)
	}

	// ---- Inputs ----
	concurrency := 5
	recurringDownloads := true
	stopRecurringDownloadsAfter := 5
	downloadInterval := 1
	stopUploadWatchersAfter := -1

	fmt.Printf("Input: \n\n%s\n\n", testMountsJson)

	var wg sync.WaitGroup

	// Trigger recurring download in a separate thread and increment the wait group counter
	wg.Add(1)
	go func() {
		// ---- Run code under test ----
		err = mainImpl(testAwsSession, debug, recurringDownloads, stopRecurringDownloadsAfter, downloadInterval, stopUploadWatchersAfter, concurrency, testMountsJson, destinationBase)
		if err != nil {
			// Fail test in case of any errors
			t.Logf("Error running the main s3-synchronizer with testMountsJson %s", testMountsJson)
			t.Errorf("Error: %v", err)
		}

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	time.Sleep(time.Duration(2*downloadInterval) * time.Second)

	// In a separate thread add/update/delete few files to the S3 location and verify that they get downloaded
	// by the recurring downloader thread after the downloadInterval
	wg.Add(1)
	go func() {
		// TEST FOR ADD -- NEW UPLOAD TO S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		// Upload same number of files in the mount again (i.e., double the noOfFilesInMount)
		testMounts[0] = *putWriteableTestMountFiles(t, testFakeBucketName, testMountId, 2*noOfFilesInMount)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the newly uploaded files are automatically downloaded after the download interval
		assertFilesDownloaded(t, testMountId, 2*noOfFilesInMount)

		// TEST FOR UPDATE -- UPLOAD TO EXISTING FILES IN S3 --> LOCAL FILE SYSTEM SYNC
		// -----------------------------------------------------------------------------

		// Update the files in S3
		updateTestMountFiles(t, testFakeBucketName, testMountId, noOfFilesInMount)

		// Sleep for the download interval duration plus some more buffer time to allow for
		// uploaded files to get downloaded
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the updated files are automatically downloaded after the download interval
		assertUpdatedFilesDownloaded(t, testMountId, noOfFilesInMount)

		// TEST FOR DELETE -- DELETE FROM S3 --> LOCAL FILE SYSTEM SYNC
		// ------------------------------------------------------------

		fileIdxToDelete := noOfFilesInMount + 1
		// Delete some files from S3 and make sure they automatically get deleted from local file system
		deleteTestMountFile(t, testFakeBucketName, testMountId, fileIdxToDelete)

		// Sleep for the download interval duration plus some more buffer time to allow sync to happen
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the file deleted from S3 are automatically deleted after the download interval
		assertFileDeleted(t, testMountId, fileIdxToDelete)

		// Decrement wait group counter to allow this test case to exit
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		// TEST FOR ADD -- NEW FILE TO LOCAL FILE SYSTEM --> S3 SYNC
		// ------------------------------------------------------------

		// Upload same number of files to the file system (i.e., double the noOfFilesInMount)
		createTestFilesLocally(t, testMountId, 2*noOfFilesInMount)

		// Sleep for some duration (e.g., download interval duration) to allow for
		// file system creation event to trigger and upload to complete
		time.Sleep(time.Duration(2*downloadInterval) * time.Second)

		// ---- Assertions ----
		// Verify that the newly created files are automatically uploaded
		assertFilesUploaded(t, testFakeBucketName, testMountId, 2*noOfFilesInMount)

		//// TEST FOR UPDATE -- UPLOAD TO EXISTING FILES IN LOCAL FILE SYSTEM --> S3 SYNC
		//// -----------------------------------------------------------------------------
		//
		//// Update the files in local file system
		//updateTestFilesLocally(t, testMountId, noOfFilesInMount)
		//
		//// Sleep for some duration (e.g., download interval duration) to allow for
		//// file system update event to trigger and upload to complete
		//time.Sleep(time.Duration(2*downloadInterval) * time.Second)
		//
		//// ---- Assertions ----
		//// Verify that the updated files are automatically uploaded
		//assertUpdatedFilesUploaded(t, testFakeBucketName, testMountId, noOfFilesInMount)
		//
		//// TEST FOR DELETE -- DELETE FROM LOCAL FILE SYSTEM --> S3 SYNC
		//// ------------------------------------------------------------
		//fileIdxToDelete := noOfFilesInMount + 1
		//// Delete some files from local file system and make sure they automatically get uploaded to S3
		//deleteTestFilesLocally(t, testMountId, fileIdxToDelete)
		//
		//// Sleep for some duration (e.g., download interval duration) to allow for
		//// file system update event to trigger and upload to complete
		//time.Sleep(time.Duration(2*downloadInterval) * time.Second)
		//
		//// ---- Assertions ----
		//// Verify that the deleted files are automatically deleted from S3
		//assertFileDeletedFromS3(t, testFakeBucketName, testMountId, fileIdxToDelete)
	}()

	wg.Wait() // Wait until all spawned go routines complete before existing the test case
}

// ------------------------------- Setup code -------------------------------/

// The main testing function that calls setup and shutdown and runs each test defined in this test file
func TestMain(m *testing.M) {
	fakeS3Server := setup()
	code := m.Run()
	shutdown(fakeS3Server)
	os.Exit(code)
}

func putReadOnlyTestMountFiles(t *testing.T, bucketName string, testMountId string, noOfFiles int) *s3Mount {
	return putTestMountFiles(t, bucketName, testMountId, noOfFiles, false)
}

func putWriteableTestMountFiles(t *testing.T, bucketName string, testMountId string, noOfFiles int) *s3Mount {
	return putTestMountFiles(t, bucketName, testMountId, noOfFiles, true)
}

func putTestMountFiles(t *testing.T, bucketName string, testMountId string, noOfFiles int, writeable bool) *s3Mount {
	s3Client := s3.New(testAwsSession)

	mountPrefix := fmt.Sprintf("studies/Organization/%s", testMountId)
	for i := 0; i < noOfFiles; i++ {
		_, err := s3Client.PutObject(&s3.PutObjectInput{
			Body:   strings.NewReader(fmt.Sprintf(testFileContentTemplate, i)),
			Bucket: aws.String(bucketName),
			Key:    aws.String(fmt.Sprintf("%s/test%d.txt", mountPrefix, i)),
		})
		if err != nil {
			// Fail test in case of any errors
			t.Errorf("Could not put test files to fake S3 server for testing: %v", err)
		}
	}
	kmsKeyId := ""
	return &s3Mount{Id: &testMountId, Bucket: &bucketName, Prefix: &mountPrefix, Writeable: &writeable, KmsKeyId: &kmsKeyId}
}

func createTestFilesLocally(t *testing.T, testMountId string, noOfFiles int) {
	for i := 0; i < noOfFiles; i++ {
		fileName := fmt.Sprintf("%s/%s/test-local%d.txt", destinationBase, testMountId, i)

		// Ensure the directory exists
		destDirPath := filepath.Dir(fileName)
		if _, err := os.Stat(destDirPath); os.IsNotExist(err) {
			os.MkdirAll(destDirPath, os.ModePerm)
		}

		content := []byte(fmt.Sprintf(testFileContentTemplate, i))
		err := ioutil.WriteFile(fileName, content, os.ModePerm)
		if err != nil {
			// Fail test in case of any errors
			t.Errorf("Could not create test files on local file system for testing: %v", err)
		}
	}
}

func updateTestMountFiles(t *testing.T, bucketName string, testMountId string, noOfFiles int) {
	s3Client := s3.New(testAwsSession)

	mountPrefix := fmt.Sprintf("studies/Organization/%s", testMountId)
	for i := 0; i < noOfFiles; i++ {
		_, err := s3Client.PutObject(&s3.PutObjectInput{
			Body:   strings.NewReader(fmt.Sprintf(testFileUpdatedContentTemplate, i)),
			Bucket: aws.String(bucketName),
			Key:    aws.String(fmt.Sprintf("%s/test%d.txt", mountPrefix, i)),
		})
		if err != nil {
			// Fail test in case of any errors
			t.Errorf("Could not put test files to fake S3 server for testing: %v", err)
		}
	}
}

func updateTestFilesLocally(t *testing.T, testMountId string, noOfFiles int) {
	for i := 0; i < noOfFiles; i++ {
		fileName := fmt.Sprintf("%s/%s/test-local%d.txt", destinationBase, testMountId, i)
		content := []byte(fmt.Sprintf(testFileUpdatedContentTemplate, i))
		err := ioutil.WriteFile(fileName, content, os.ModePerm)
		if err != nil {
			// Fail test in case of any errors
			t.Errorf("Could not update test files on local file system for testing: %v", err)
		}
	}
}

func deleteTestMountFile(t *testing.T, bucketName string, testMountId string, fileIdx int) {
	s3Client := s3.New(testAwsSession)

	mountPrefix := fmt.Sprintf("studies/Organization/%s", testMountId)
	_, err := s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(fmt.Sprintf("%s/test%d.txt", mountPrefix, fileIdx)),
	})
	if err != nil {
		// Fail test in case of any errors
		t.Errorf("Could not delete test files from fake S3 server for testing: %v", err)
	}
}

func deleteTestFilesLocally(t *testing.T, testMountId string, fileIdx int) {
	fileName := fmt.Sprintf("%s/%s/test-local%d.txt", destinationBase, testMountId, fileIdx)
	err := os.Remove(fileName)
	if err != nil {
		// Fail test in case of any errors
		t.Errorf("Could not delete test file from local file system for testing: %v", err)
	}
}

func assertFilesDownloaded(t *testing.T, testMountId string, noOfFiles int) {
	assertFilesDownloadedWithContent(t, testMountId, noOfFiles, testFileContentTemplate)
}

func assertUpdatedFilesDownloaded(t *testing.T, testMountId string, noOfFiles int) {
	assertFilesDownloadedWithContent(t, testMountId, noOfFiles, testFileUpdatedContentTemplate)
}

func assertFilesDownloadedWithContent(t *testing.T, testMountId string, noOfFiles int, expectedContentTemplate string) {
	for i := 0; i < noOfFiles; i++ {
		expectedFile := fmt.Sprintf("%s/%s/test%d.txt", destinationBase, testMountId, i)
		expectedFileContent := fmt.Sprintf(expectedContentTemplate, i)
		if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
			t.Errorf(`ASSERT_FAILURE: Expected: File "%v" to exist after download | Actual: The file not found`, expectedFile)
		} else {
			// If file exists then verify its contents
			fileContentBytes, err := ioutil.ReadFile(expectedFile)
			fileContent := string(fileContentBytes)
			if err == nil {
				if expectedFileContent != fileContent {
					t.Errorf(`ASSERT_FAILURE: CONTENT_MISMATCH: Expected: File "%v" to contain text "%v" | Actual: The file contains "%v" instead`, expectedFile, expectedFileContent, fileContent)
				}
			} else {
				t.Errorf("Could not read file: %v | Error: %v", expectedFile, err)
			}
		}
	}
}

func assertFilesUploaded(t *testing.T, bucketName string, testMountId string, noOfFiles int) {
	assertFilesUploadedWithContent(t, bucketName, testMountId, noOfFiles, testFileContentTemplate)
}

func assertUpdatedFilesUploaded(t *testing.T, bucketName string, testMountId string, noOfFiles int) {
	assertFilesUploadedWithContent(t, bucketName, testMountId, noOfFiles, testFileUpdatedContentTemplate)
}

func assertFilesUploadedWithContent(t *testing.T, bucketName string, testMountId string, noOfFiles int, expectedContentTemplate string) {
	s3Client := s3.New(testAwsSession)
	mountPrefix := fmt.Sprintf("studies/Organization/%s", testMountId)

	for i := 0; i < noOfFiles; i++ {
		key := fmt.Sprintf("%s/test-local%d.txt", mountPrefix, i)
		resp, err := s3Client.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
		})
		if err != nil {
			// Fail test in case of any errors
			t.Errorf("Could not put get objects from fake S3 server for testing: %v", err)
		}

		fmt.Printf("\n\nkey == %v, resp == %v\n\n", key, resp)

		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		fileContentInS3 := buf.String()
		expectedFileContent := fmt.Sprintf(expectedContentTemplate, i)
		if expectedFileContent != fileContentInS3 {
			t.Errorf(`ASSERT_FAILURE: CONTENT_MISMATCH: Expected: S3 file "%v" to contain text "%v" | Actual: It contains "%v" instead`, key, expectedFileContent, fileContentInS3)
		}
	}
}

func assertFileDeleted(t *testing.T, testMountId string, fileIdx int) {
	expectedFile := fmt.Sprintf("%s/%s/test%d.txt", destinationBase, testMountId, fileIdx)
	if _, err := os.Stat(expectedFile); !os.IsNotExist(err) {
		t.Errorf(`ASSERT_FAILURE: Expected: File "%v" to NOT exist after sync | Actual: The file exists`, expectedFile)
	}
}

func assertFileDeletedFromS3(t *testing.T, bucketName string, testMountId string, fileIdx int) {
	mountPrefix := fmt.Sprintf("studies/Organization/%s", testMountId)
	key := fmt.Sprintf("%s/test-local%d.txt", mountPrefix, fileIdx)
	s3Client := s3.New(testAwsSession)
	resp, err := s3Client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(key),
	})
	if err != nil {
		// Fail test in case of any errors
		t.Errorf("Could not list files from fake S3 server for testing: %v", err)
	}
	if len(resp.Contents) > 0 {
		t.Errorf(`ASSERT_FAILURE: Expected: File "%v" to NOT exist in S3 after sync | Actual: The file exists`, key)
	}
}

func setup() *httptest.Server {
	// fake s3
	backend := s3mem.New()
	faker := gofakes3.New(backend)
	fakeS3Server := httptest.NewServer(faker.Server())
	testAwsSession = makeTestSession(fakeS3Server)

	createFakeS3BucketForTesting()

	return fakeS3Server
}

func createFakeS3BucketForTesting() {
	s3Client := s3.New(testAwsSession)
	params := &s3.CreateBucketInput{
		Bucket: aws.String(testFakeBucketName),
	}
	_, err := s3Client.CreateBucket(params)
	if err != nil {
		// Fail test in case of any errors
		fmt.Printf("\n\nCould not create bucket using fake S3 server for testing: %v\n\n", err)

		// Exit program with non-zero exit code
		// Cannot use "t.Errorf" to fail here since this is executed from setup
		os.Exit(1)
	}
}

func shutdown(fakeS3Server *httptest.Server) {
	fakeS3Server.Close()

	// delete all temporary output files created under destinationBase
	err := os.RemoveAll(destinationBase)
	if err != nil {
		fmt.Printf("\n\nError cleaning up the temporary output directory '%s': %v\n\n", destinationBase, err)

		// Exit program with non-zero exit code
		// Cannot use "t.Errorf" to fail here since this is executed from shutdown
		os.Exit(1)
	}
}

func makeTestSession(fakeS3Server *httptest.Server) *session.Session {
	var sess *session.Session
	s3Config := &aws.Config{
		Credentials:      credentials.NewStaticCredentials("FAKE-ACCESSKEYID", "FAKE-SECRETACCESSKEY", ""),
		Endpoint:         aws.String(fakeS3Server.URL),
		Region:           aws.String(testRegion),
		DisableSSL:       aws.Bool(true),
		S3ForcePathStyle: aws.Bool(true),
	}
	s3Config.WithS3ForcePathStyle(true)

	sess = session.Must(session.NewSessionWithOptions(session.Options{
		Config: *s3Config,
	}))
	return sess
}
