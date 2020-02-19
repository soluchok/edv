/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package startcmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/trustbloc/edge-core/pkg/storage"
	couchdbstore "github.com/trustbloc/edge-core/pkg/storage/couchdb"
	"github.com/trustbloc/edge-core/pkg/storage/memstore"

	"github.com/trustbloc/edv/pkg/restapi/edv"
	cmdutils "github.com/trustbloc/edv/pkg/utils/cmd"
)

const (
	hostURLFlagName      = "host-url"
	hostURLFlagShorthand = "u"
	hostURLFlagUsage     = "URL to run the edv instance on. Format: HostName:Port."
	hostURLEnvKey        = "EDV_HOST_URL"

	databaseTypeFlagName      = "database-type"
	databaseTypeFlagShorthand = "t"
	databaseTypeFlagUsage     = "The type of database to use internally in the EDV. Supported options: mem, couchdb."
	databaseTypeEnvKey        = "EDV_DATABASE_TYPE"

	databaseTypeMemOption     = "mem"
	databaseTypeCouchDBOption = "couchdb"

	databaseURLFlagName      = "database-url"
	databaseURLFlagShorthand = "l"
	databaseURLFlagUsage     = "The URL of the database. Not needed if using memstore." +
		"For CouchDB, include the username:password@ text if required."
	databaseURLEnvKey = "EDV_DATABASE_URL"
)

var errMissingHostURL = fmt.Errorf("host URL not provided")
var errInvalidDatabaseType = fmt.Errorf("database type not set to a valid type." +
	" run start --help to see the available options")
var errMissingDatabaseURL = fmt.Errorf("couchDB database URL not set")

type edvParameters struct {
	srv          server
	hostURL      string
	databaseType string
	databaseURL  string
}

type server interface {
	ListenAndServe(host string, router http.Handler) error
}

// HTTPServer represents an actual HTTP server implementation.
type HTTPServer struct{}

// ListenAndServe starts the server using the standard Go HTTP server implementation.
func (s *HTTPServer) ListenAndServe(host string, router http.Handler) error {
	return http.ListenAndServe(host, router)
}

// GetStartCmd returns the Cobra start command.
func GetStartCmd(srv server) *cobra.Command {
	startCmd := createStartCmd(srv)

	createFlags(startCmd)

	return startCmd
}

func createStartCmd(srv server) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start EDV",
		Long:  "Start EDV",
		RunE: func(cmd *cobra.Command, args []string) error {
			hostURL, err := cmdutils.GetUserSetVar(cmd, hostURLFlagName, hostURLEnvKey, false)
			if err != nil {
				return err
			}

			databaseType, err := cmdutils.GetUserSetVar(cmd, databaseTypeFlagName, databaseTypeEnvKey, false)
			if err != nil {
				return err
			}

			databaseURL, err := cmdutils.GetUserSetVar(cmd, databaseURLFlagName, databaseURLEnvKey, true)
			if err != nil {
				return err
			}

			parameters := &edvParameters{
				srv:          srv,
				hostURL:      hostURL,
				databaseType: databaseType,
				databaseURL:  databaseURL,
			}
			return startEDV(parameters)
		},
	}
}

func createFlags(startCmd *cobra.Command) {
	startCmd.Flags().StringP(hostURLFlagName, hostURLFlagShorthand, "", hostURLFlagUsage)
	startCmd.Flags().StringP(databaseTypeFlagName, databaseTypeFlagShorthand, "", databaseTypeFlagUsage)
	startCmd.Flags().StringP(databaseURLFlagName, databaseURLFlagShorthand, "", databaseURLFlagUsage)
}

func startEDV(parameters *edvParameters) error {
	if parameters.hostURL == "" {
		return errMissingHostURL
	}

	provider, err := createProvider(parameters)
	if err != nil {
		return err
	}

	edvService, err := edv.New(provider)
	if err != nil {
		return err
	}

	handlers := edvService.GetOperations()
	router := mux.NewRouter()
	router.UseEncodedPath()

	for _, handler := range handlers {
		router.HandleFunc(handler.Path(), handler.Handle()).Methods(handler.Method())
	}

	log.Infof("Starting edv rest server on host %s", parameters.hostURL)
	err = parameters.srv.ListenAndServe(parameters.hostURL, router)

	return err
}

func createProvider(parameters *edvParameters) (storage.Provider, error) {
	var provider storage.Provider

	switch {
	case strings.EqualFold(parameters.databaseType, databaseTypeMemOption):
		provider = memstore.NewProvider()
	case strings.EqualFold(parameters.databaseType, databaseTypeCouchDBOption):
		couchDBProvider, err := couchdbstore.NewProvider(parameters.databaseURL)
		if err != nil {
			if err.Error() == "hostURL for new CouchDB provider can't be blank" {
				return nil, errMissingDatabaseURL
			}

			return nil, err
		}

		provider = couchDBProvider
	default:
		return nil, errInvalidDatabaseType
	}

	return provider, nil
}
