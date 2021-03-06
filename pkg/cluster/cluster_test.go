package cluster

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/flosch/pongo2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/Juniper/contrail/pkg/apisrv"
	//"github.com/Juniper/contrail/pkg/common"
	//log "github.com/sirupsen/logrus"
)

const (
	clusterID = "test_cluster_uuid"
)

func TestMain(m *testing.M) {
	apisrv.SetupAndRunTest(m)
}

func verifyEndpoints(t *testing.T, testScenario *apisrv.TestScenario) bool {
	for _, client := range testScenario.Clients {
		var response map[string]interface{}
		url := fmt.Sprintf("/endpoints?parent_uuid=%s", clusterID)
		_, err := client.Read(url, &response)
		assert.NoError(t, err, "Unable to list endpoints of the cluster")
	}
	return true
}

func verifyClusterDeleted(t *testing.T, testScenario *apisrv.TestScenario) bool {
	// Make sure working dir is deleted
	if _, err := os.Stat(defaultWorkRoot + "/" + clusterID); err == nil {
		// working dir not deleted
		return false
	}
	// Make sure endpoints are deleted
	return verifyEndpoints(t, testScenario)
}

func compareInstances(t *testing.T, generated, expected string) bool {
	generatedInstances, err := ioutil.ReadFile(generated)
	assert.NoError(t, err, "Unable to read generated instances.yml")
	expectedInstances, err := ioutil.ReadFile(expected)
	assert.NoError(t, err, "Unable to read expected instances.yml")
	return bytes.Equal(generatedInstances, expectedInstances)
}

func runClusterTest(t *testing.T, testInput, expectedOutput string,
	context map[string]interface{}) {
	// mock keystone to let access server after cluster create
	keystoneAuthURL := viper.GetString("keystone.authurl")
	ksPublic := apisrv.MockServerWithKeystone("127.0.0.1:35357", keystoneAuthURL)
	defer ksPublic.Close()
	ksPrivate := apisrv.MockServerWithKeystone("127.0.0.1:5000", keystoneAuthURL)
	defer ksPrivate.Close()

	// Create the cluster and related objects
	var testScenario apisrv.TestScenario
	err := apisrv.LoadTestScenario(&testScenario, testInput, context)
	assert.NoError(t, err, "failed to load cluster test data")
	apisrv.RunTestScenario(t, &testScenario)
	// create cluster config
	config := &Config{
		ID:           "alice",
		Password:     "alice_password",
		ProjectID:    "admin",
		AuthURL:      apisrv.TestServer.URL + "/keystone/v3",
		Endpoint:     apisrv.TestServer.URL,
		InSecure:     true,
		ClusterID:    clusterID,
		Action:       "create",
		LogLevel:     "debug",
		TemplateRoot: "configs/",
		Test:         true,
	}
	// create cluster
	clusterManager, err := NewCluster(config)
	assert.NoError(t, err, "failed to create cluster manager to create cluster")
	err = clusterManager.Manage()
	assert.NoError(t, err, "failed to manage(create) cluster")
	// compare the instances.yml with expected
	generatedFile := defaultWorkRoot + "/" + clusterID + "/instances.yml"
	assert.True(t, compareInstances(t, generatedFile, expectedOutput),
		"Instance file created during cluster create is not as expected")
	// delete cluster
	config.Action = "delete"
	clusterManager, err = NewCluster(config)
	assert.NoError(t, err, "failed to create cluster manager to delete cluster")
	err = clusterManager.Manage()
	assert.NoError(t, err, "failed to manage(delete) cluster")
	// make sure cluster is removed
	assert.True(t, verifyClusterDeleted(t, &testScenario),
		"Instance file is not deleted during cluster delete")
}

func TestAllInOneCluster(t *testing.T) {
	context := pongo2.Context{
		"control_data_network_list": "",
	}
	runClusterTest(t,
		"./test_data/test_all_in_one_cluster.tmpl",
		"./test_data/expected_all_in_one_instances.yml",
		context)
}

func TestClusterWithManagementNetworkAsControlDataNet(t *testing.T) {
	context := pongo2.Context{
		"control_data_network_list": "127.0.0.0/24",
	}
	runClusterTest(t,
		"./test_data/test_all_in_one_cluster.tmpl",
		"./test_data/expected_same_mgmt_ctrldata_net_instances.yml",
		context)
}

func TestClusterWithSeperateManagementAndControlDataNet(t *testing.T) {
	context := pongo2.Context{
		"control_data_network_list": "10.1.0.0/24",
	}
	runClusterTest(t,
		"./test_data/test_all_in_one_cluster.tmpl",
		"./test_data/expected_multi_interface_instances.yml",
		context)
}
