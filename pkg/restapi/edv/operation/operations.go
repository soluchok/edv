/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package operation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/trustbloc/edge-core/pkg/storage"

	"github.com/trustbloc/edv/pkg/internal/common/support"
)

const (
	vaultIDPathVariable = "vaultID"
	docIDPathVariable   = "docID"

	createVaultEndpoint    = "/data-vaults"
	createDocumentEndpoint = "/encrypted-data-vaults/{" + vaultIDPathVariable + "}/docs"
	readDocumentEndpoint   = "/encrypted-data-vaults/{" + vaultIDPathVariable + "}/docs/{" + docIDPathVariable + "}"

	// VaultNotFoundErrMsg is the message returned by the EDV server when a vault can't be found.
	VaultNotFoundErrMsg = "specified vault does not exist"
	// DocumentNotFoundErrMsg is the message returned by the EDV server when a document can't be found
	// within the given vault.
	DocumentNotFoundErrMsg = "specified document does not exist"
	// DuplicateVaultErrMsg is the message returned by the EDV server when an attempt is made to create a vault with
	// the same ID as an already existing vault.
	DuplicateVaultErrMsg = "vault already exists"
	// DuplicateDocumentErrMsg is the message returned by the EDV server when an attempt is made to create a document with
	// the same ID as an already existing document inside the vault.
	DuplicateDocumentErrMsg = "a document with the given id already exists"
)

var errVaultNotFound = fmt.Errorf("%s", VaultNotFoundErrMsg)
var errDocumentNotFound = fmt.Errorf("%s", DocumentNotFoundErrMsg)
var errDuplicateVault = fmt.Errorf("%s", DuplicateVaultErrMsg)
var errDuplicateDocument = fmt.Errorf("%s", DuplicateDocumentErrMsg)

// Handler http handler for each controller API endpoint
type Handler interface {
	Path() string
	Method() string
	Handle() http.HandlerFunc
}

// New returns EDV instance
func New(provider storage.Provider) *Operation {
	svc := &Operation{
		vaultCollection: VaultCollection{
			provider:   provider,
			openStores: make(map[string]storage.Store),
		}}
	svc.registerHandler()

	return svc
}

// Operation defines handlers for EDV service
type Operation struct {
	handlers        []Handler
	vaultCollection VaultCollection
}

// VaultCollection represents EDV storage.
type VaultCollection struct {
	provider   storage.Provider
	openStores map[string]storage.Store
}

func (c *Operation) createDataVaultHandler(rw http.ResponseWriter, req *http.Request) {
	config := DataVaultConfiguration{}

	err := json.NewDecoder(req.Body).Decode(&config)

	blankReferenceIDProvided := err == nil && config.ReferenceID == ""

	if err != nil || blankReferenceIDProvided {
		rw.WriteHeader(http.StatusBadRequest)

		var errMsg string
		if blankReferenceIDProvided {
			errMsg = "referenceId can't be blank"
		} else {
			errMsg = err.Error()
		}

		_, err = rw.Write([]byte(errMsg))
		if err != nil {
			log.Errorf("Failed to write response for data vault creation failure due to the provided"+
				" data vault configuration: %s", err.Error())
		}

		return
	}

	err = c.vaultCollection.createDataVault(config.ReferenceID)
	if err != nil {
		if err == errDuplicateVault {
			rw.WriteHeader(http.StatusConflict)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		_, err = rw.Write([]byte(fmt.Sprintf("Data vault creation failed: %s", err)))
		if err != nil {
			log.Errorf("Failed to write response for data vault creation failure: %s", err.Error())
		}

		return
	}

	urlEncodedReferenceID := url.PathEscape(config.ReferenceID)

	rw.Header().Set("Location", req.Host+"/encrypted-data-vaults/"+urlEncodedReferenceID)
	rw.WriteHeader(http.StatusCreated)
}

func (c *Operation) createDocumentHandler(rw http.ResponseWriter, req *http.Request) {
	incomingDocument := StructuredDocument{}

	err := json.NewDecoder(req.Body).Decode(&incomingDocument)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			log.Errorf("Failed to write response for document creation failure: %s", err.Error())
		}

		return
	}

	vaultID, success := unescapePathVar(vaultIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	err = c.vaultCollection.createDocument(vaultID, incomingDocument)
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			log.Errorf(
				"Failed to write response for document creation failure: %s", err.Error())
		}

		return
	}

	rw.Header().Set("Location", req.Host+"/encrypted-data-vaults/"+
		url.PathEscape(vaultID)+"/docs/"+url.PathEscape(incomingDocument.ID))
	rw.WriteHeader(http.StatusCreated)
}

func (c *Operation) readDocumentHandler(rw http.ResponseWriter, req *http.Request) {
	vaultID, success := unescapePathVar(vaultIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	docID, success := unescapePathVar(docIDPathVariable, mux.Vars(req), rw)
	if !success {
		return
	}

	documentJSON, err := c.vaultCollection.readDocument(vaultID, docID)
	if err != nil {
		if err == errDocumentNotFound || err == errVaultNotFound {
			rw.WriteHeader(http.StatusNotFound)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		_, err = rw.Write([]byte(err.Error()))
		if err != nil {
			log.Errorf("Failed to write response for document retrieval failure: %s", err.Error())
		}

		return
	}

	_, err = rw.Write(documentJSON)
	if err != nil {
		log.Errorf("Failed to write response for document retrieval success: %s", err.Error())
	}
}

func (vc *VaultCollection) createDataVault(id string) error {
	_, exists := vc.openStores[id]
	if exists {
		return errDuplicateVault
	}

	store, err := vc.provider.OpenStore(id)
	if err != nil {
		return err
	}

	vc.openStores[id] = store

	return nil
}

func (vc *VaultCollection) createDocument(vaultID string, document StructuredDocument) error {
	vault, exists := vc.openStores[vaultID]
	if !exists {
		return errVaultNotFound
	}

	// The Create Document API call should not overwrite an existing document.
	// So we first check to make sure there is not already a document associated with the id.
	// If there is, we send back an error.
	_, err := vault.Get(document.ID)
	if err == nil {
		return errDuplicateDocument
	} else if err != storage.ErrValueNotFound {
		return err
	}

	documentJSON, err := json.Marshal(document)
	if err != nil {
		return err
	}

	return vault.Put(document.ID, documentJSON)
}

func (vc *VaultCollection) readDocument(vaultID, docID string) ([]byte, error) {
	vault, exists := vc.openStores[vaultID]
	if !exists {
		return nil, errVaultNotFound
	}

	documentJSON, err := vault.Get(docID)
	if err == storage.ErrValueNotFound {
		return nil, errDocumentNotFound // Returns a more specific error message
	} else if err != nil {
		return nil, err
	}

	return documentJSON, err
}

// registerHandler register handlers to be exposed from this service as REST API endpoints
func (c *Operation) registerHandler() {
	// Add more protocol endpoints here to expose them as controller API endpoints
	c.handlers = []Handler{
		support.NewHTTPHandler(createVaultEndpoint, http.MethodPost, c.createDataVaultHandler),
		support.NewHTTPHandler(createDocumentEndpoint, http.MethodPost, c.createDocumentHandler),
		support.NewHTTPHandler(readDocumentEndpoint, http.MethodGet, c.readDocumentHandler),
	}
}

// GetRESTHandlers get all controller API handler available for this service
func (c *Operation) GetRESTHandlers() []Handler {
	return c.handlers
}

// Unescapes the given path variable from the vars map and writes a response if any failure occurs.
// Returns the unescaped version of the path variable and a bool indicating whether the unescaping was successful.
func unescapePathVar(pathVar string, vars map[string]string, rw http.ResponseWriter) (string, bool) {
	unescapedPathVar, err := url.PathUnescape(vars[pathVar])
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)

		_, err = rw.Write([]byte(fmt.Sprintf("unable to escape %s path variable: %s", pathVar, err.Error())))
		if err != nil {
			log.Errorf("Failed to write response for %s unescaping failure: %s", pathVar, err.Error())
		}

		return "", false
	}

	return unescapedPathVar, true
}
