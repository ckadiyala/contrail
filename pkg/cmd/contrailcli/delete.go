package contrailcli

import (
	"fmt"
	"net/http"

	"github.com/Juniper/contrail/pkg/common"
	"github.com/k0kubun/pp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func init() {
	ContrailCLI.AddCommand(DeleteCmd)
}

// DeleteCmd defines delete command.
var DeleteCmd = &cobra.Command{
	Use:   "delete [FilePath]",
	Short: "Delete resources specified in given YAML file",
	Long:  "Use resource format just like in 'schema' command output",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		response, err := deleteResources(args[0])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(response)
	},
}

func deleteResources(dataPath string) (string, error) {
	client, err := getClient()
	if err != nil {
		return "", nil
	}
	request, err := readResources(dataPath)
	if err != nil {
		return "", nil
	}
	_, err = pp.Println(request)
	if err != nil {
		fmt.Printf("Error printing request: %v", err)
	}
	for i := len(request.Resources) - 1; i >= 0; i-- {
		resource := request.Resources[i]
		uuid, err := common.GetUUIDFromInterface(resource.Data)
		if err != nil {
			return "", err
		}
		var output interface{}
		response, err := client.Delete(path(resource.Kind, uuid), &output)
		if response.StatusCode != http.StatusNotFound && err != nil {
			return "", err
		}
	}
	return "", nil
}
