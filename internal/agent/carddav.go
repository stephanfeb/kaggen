package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav/carddav"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/oauth"
)

const (
	carddavDefaultLimit = 25
	carddavMaxLimit     = 100
)

// CardDAVToolArgs defines the input arguments for the carddav tool.
type CardDAVToolArgs struct {
	// Required fields
	Action   string `json:"action" jsonschema:"required,description=Action to perform: list_addressbooks search_contacts get_contact create_contact update_contact delete_contact,enum=list_addressbooks,enum=search_contacts,enum=get_contact,enum=create_contact,enum=update_contact,enum=delete_contact"`
	Provider string `json:"provider" jsonschema:"required,description=Provider name (e.g. google icloud fastmail) for server configuration and auth"`

	// Authentication
	Email string `json:"email,omitempty" jsonschema:"description=Email/username for authentication. Required for OAuth providers"`

	// Address book selection
	AddressBook string `json:"address_book,omitempty" jsonschema:"description=Address book name or path. Default: primary/default address book"`

	// Contact identification (for get/update/delete)
	UID string `json:"uid,omitempty" jsonschema:"description=Contact UID. Required for get_contact update_contact and delete_contact"`

	// Contact fields (for create/update)
	FullName     string   `json:"full_name,omitempty" jsonschema:"description=Full display name (FN field). Required for create_contact"`
	FirstName    string   `json:"first_name,omitempty" jsonschema:"description=First/given name"`
	LastName     string   `json:"last_name,omitempty" jsonschema:"description=Last/family name"`
	Nickname     string   `json:"nickname,omitempty" jsonschema:"description=Nickname or alias"`
	Emails       []string `json:"emails,omitempty" jsonschema:"description=Email addresses"`
	Phones       []string `json:"phones,omitempty" jsonschema:"description=Phone numbers"`
	Organization string   `json:"organization,omitempty" jsonschema:"description=Company/organization name"`
	Title        string   `json:"title,omitempty" jsonschema:"description=Job title"`
	Notes        string   `json:"notes,omitempty" jsonschema:"description=Additional notes"`

	// Search fields
	Query string `json:"query,omitempty" jsonschema:"description=Search query text (matches name email phone)"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum contacts to return (default: 25 max: 100)"`
}

// CardDAVToolResult is the result of a CardDAV operation.
type CardDAVToolResult struct {
	Success      bool              `json:"success"`
	Message      string            `json:"message"`
	Contact      *Contact          `json:"contact,omitempty"`       // For get_contact, create_contact, update_contact
	Contacts     []Contact         `json:"contacts,omitempty"`      // For search_contacts
	AddressBooks []AddressBookInfo `json:"address_books,omitempty"` // For list_addressbooks
}

// Contact represents a contact/address entry.
type Contact struct {
	UID          string   `json:"uid"`
	FullName     string   `json:"full_name"`
	FirstName    string   `json:"first_name,omitempty"`
	LastName     string   `json:"last_name,omitempty"`
	Nickname     string   `json:"nickname,omitempty"`
	Emails       []string `json:"emails,omitempty"`
	Phones       []string `json:"phones,omitempty"`
	Organization string   `json:"organization,omitempty"`
	Title        string   `json:"title,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	ETag         string   `json:"etag,omitempty"`
}

// AddressBookInfo describes an available address book.
type AddressBookInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// NewCardDAVTool creates a CardDAV tool with OAuth and Basic auth support.
func NewCardDAVTool(
	userID string,
	allowedProviders []string,
	secrets map[string]string,
	tokenGetter DAVTokenGetter,
	providerGetter DAVProviderGetter,
) tool.CallableTool {
	allowed := make(map[string]bool)
	for _, p := range allowedProviders {
		allowed[p] = true
	}

	return function.NewFunctionTool(
		func(ctx context.Context, args CardDAVToolArgs) (*CardDAVToolResult, error) {
			return executeCardDAVTool(ctx, args, userID, allowed, secrets, tokenGetter, providerGetter)
		},
		function.WithName("carddav"),
		function.WithDescription("Manage contacts via CardDAV. Actions: list_addressbooks, search_contacts, get_contact, create_contact, update_contact, delete_contact. Requires OAuth authorization or Basic auth credentials."),
	)
}

func executeCardDAVTool(
	ctx context.Context,
	args CardDAVToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter DAVTokenGetter,
	providerGetter DAVProviderGetter,
) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	// Validate provider is allowed
	if len(allowedProviders) > 0 && !allowedProviders[args.Provider] {
		result.Message = fmt.Sprintf("Provider %q not available to this skill", args.Provider)
		return result, nil
	}

	// Get provider configuration
	if providerGetter == nil {
		result.Message = "Provider configuration not available"
		return result, nil
	}
	provider, ok := providerGetter(args.Provider)
	if !ok {
		result.Message = fmt.Sprintf("Provider %q not configured", args.Provider)
		return result, nil
	}

	// Build DAV client configuration
	clientCfg, err := buildCardDAVClientConfig(ctx, args, userID, provider, secrets, tokenGetter)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Create CardDAV client
	httpClient := NewDAVHTTPClient(clientCfg)
	client, err := carddav.NewClient(httpClient, clientCfg.ServerURL)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create CardDAV client: %v", err)
		return result, nil
	}

	// Execute action
	switch args.Action {
	case "list_addressbooks":
		return listAddressBooksAction(ctx, client, args)
	case "search_contacts":
		return searchContactsAction(ctx, client, args)
	case "get_contact":
		return getContactAction(ctx, client, args)
	case "create_contact":
		return createContactAction(ctx, client, args)
	case "update_contact":
		return updateContactAction(ctx, client, args)
	case "delete_contact":
		return deleteContactAction(ctx, client, args)
	default:
		result.Message = fmt.Sprintf("Unknown action %q", args.Action)
		return result, nil
	}
}

func buildCardDAVClientConfig(
	ctx context.Context,
	args CardDAVToolArgs,
	userID string,
	provider config.OAuthProvider,
	secrets map[string]string,
	tokenGetter DAVTokenGetter,
) (DAVClientConfig, error) {
	cfg := DAVClientConfig{}

	// Resolve server URL
	serverURL, err := ResolveDAVServerURL(ctx, provider, args.Email, "carddav")
	if err != nil {
		return cfg, fmt.Errorf("failed to resolve CardDAV server: %v", err)
	}
	cfg.ServerURL = serverURL

	// Try OAuth first if token getter is available
	if tokenGetter != nil && args.Email != "" {
		token, err := tokenGetter(userID, args.Provider)
		if err == nil {
			cfg.AuthType = DAVAuthOAuth
			cfg.OAuthToken = token.AccessToken
			cfg.UserID = userID
			cfg.Provider = args.Provider
			return cfg, nil
		}
		// If token not found, fall through to try basic auth
		if err != oauth.ErrTokenNotFound && err != oauth.ErrTokenExpired {
			return cfg, fmt.Errorf("OAuth token retrieval failed: %v", err)
		}
	}

	// Try Basic auth from secrets
	usernameKey := args.Provider + "-carddav-username"
	passwordKey := args.Provider + "-carddav-password"
	if username, ok := secrets[usernameKey]; ok {
		if password, ok := secrets[passwordKey]; ok {
			cfg.AuthType = DAVAuthBasic
			cfg.Username = username
			cfg.Password = password
			return cfg, nil
		}
	}

	// No auth available
	if tokenGetter != nil {
		return cfg, fmt.Errorf("OAuth authorization required for %s. Please authorize via dashboard", args.Provider)
	}
	return cfg, fmt.Errorf("no authentication configured for %s", args.Provider)
}

// listAddressBooksAction lists available address books.
func listAddressBooksAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	// Find user principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to find user principal: %v", err)
		return result, nil
	}

	// Find address book home
	homeSet, err := client.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to find address book home: %v", err)
		return result, nil
	}

	// List address books
	addressBooks, err := client.FindAddressBooks(ctx, homeSet)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to list address books: %v", err)
		return result, nil
	}

	result.AddressBooks = make([]AddressBookInfo, 0, len(addressBooks))
	for _, ab := range addressBooks {
		info := AddressBookInfo{
			Name: ab.Name,
			Path: ab.Path,
		}
		if ab.Description != "" {
			info.Description = ab.Description
		}
		result.AddressBooks = append(result.AddressBooks, info)
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d address books", len(result.AddressBooks))
	return result, nil
}

// searchContactsAction searches contacts by query.
func searchContactsAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	if args.Query == "" {
		result.Message = "Error: 'query' is required for search_contacts action"
		return result, nil
	}

	// Get address book path
	abPath, err := resolveAddressBookPath(ctx, client, args.AddressBook)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Set limit
	limit := args.Limit
	if limit <= 0 {
		limit = carddavDefaultLimit
	}
	if limit > carddavMaxLimit {
		limit = carddavMaxLimit
	}

	// Build search query - search in FN (formatted name), EMAIL, and TEL
	query := &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{
			Props: []string{
				vcard.FieldUID,
				vcard.FieldFormattedName,
				vcard.FieldName,
				vcard.FieldNickname,
				vcard.FieldEmail,
				vcard.FieldTelephone,
				vcard.FieldOrganization,
				vcard.FieldTitle,
				vcard.FieldNote,
			},
		},
		PropFilters: []carddav.PropFilter{
			{
				Name: vcard.FieldFormattedName,
				TextMatches: []carddav.TextMatch{
					{
						Text:      args.Query,
						MatchType: carddav.MatchContains,
					},
				},
			},
		},
		FilterTest: carddav.FilterAnyOf,
	}

	objects, err := client.QueryAddressBook(ctx, abPath, query)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to search contacts: %v", err)
		return result, nil
	}

	result.Contacts = make([]Contact, 0, len(objects))
	for i, obj := range objects {
		if i >= limit {
			break
		}
		contact := parseAddressObject(obj)
		if contact != nil {
			result.Contacts = append(result.Contacts, *contact)
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d contacts matching '%s'", len(result.Contacts), args.Query)
	return result, nil
}

// getContactAction retrieves a single contact by UID.
func getContactAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for get_contact action"
		return result, nil
	}

	// Get address book path
	abPath, err := resolveAddressBookPath(ctx, client, args.AddressBook)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Get the contact
	contactPath := abPath + args.UID + ".vcf"
	objects, err := client.MultiGetAddressBook(ctx, abPath, &carddav.AddressBookMultiGet{
		Paths: []string{contactPath},
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get contact: %v", err)
		return result, nil
	}

	if len(objects) == 0 {
		result.Message = fmt.Sprintf("Contact with UID %q not found", args.UID)
		return result, nil
	}

	contact := parseAddressObject(objects[0])
	if contact == nil {
		result.Message = "Failed to parse contact data"
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Retrieved contact: %s", contact.FullName)
	result.Contact = contact
	return result, nil
}

// createContactAction creates a new contact.
func createContactAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	// Validate required fields
	if args.FullName == "" {
		result.Message = "Error: 'full_name' is required for create_contact action"
		return result, nil
	}

	// Get address book path
	abPath, err := resolveAddressBookPath(ctx, client, args.AddressBook)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Generate UID if not provided
	uid := args.UID
	if uid == "" {
		uid = fmt.Sprintf("%d-%s@kaggen", time.Now().UnixNano(), generateRandomString(8))
	}

	// Build vCard
	card := buildVCard(uid, args.FullName, args.FirstName, args.LastName, args.Nickname,
		args.Emails, args.Phones, args.Organization, args.Title, args.Notes)

	// Create the contact
	contactPath := abPath + uid + ".vcf"
	_, err = client.PutAddressObject(ctx, contactPath, card)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create contact: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Created contact: %s", args.FullName)
	result.Contact = &Contact{
		UID:          uid,
		FullName:     args.FullName,
		FirstName:    args.FirstName,
		LastName:     args.LastName,
		Nickname:     args.Nickname,
		Emails:       args.Emails,
		Phones:       args.Phones,
		Organization: args.Organization,
		Title:        args.Title,
		Notes:        args.Notes,
	}
	return result, nil
}

// updateContactAction updates an existing contact.
func updateContactAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for update_contact action"
		return result, nil
	}

	// Get address book path
	abPath, err := resolveAddressBookPath(ctx, client, args.AddressBook)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// First, get the existing contact
	contactPath := abPath + args.UID + ".vcf"
	objects, err := client.MultiGetAddressBook(ctx, abPath, &carddav.AddressBookMultiGet{
		Paths: []string{contactPath},
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get existing contact: %v", err)
		return result, nil
	}

	if len(objects) == 0 {
		result.Message = fmt.Sprintf("Contact with UID %q not found", args.UID)
		return result, nil
	}

	existingContact := parseAddressObject(objects[0])
	if existingContact == nil {
		result.Message = "Failed to parse existing contact"
		return result, nil
	}

	// Merge updates
	fullName := args.FullName
	if fullName == "" {
		fullName = existingContact.FullName
	}
	firstName := args.FirstName
	if firstName == "" {
		firstName = existingContact.FirstName
	}
	lastName := args.LastName
	if lastName == "" {
		lastName = existingContact.LastName
	}
	nickname := args.Nickname
	if nickname == "" {
		nickname = existingContact.Nickname
	}
	emails := args.Emails
	if len(emails) == 0 {
		emails = existingContact.Emails
	}
	phones := args.Phones
	if len(phones) == 0 {
		phones = existingContact.Phones
	}
	organization := args.Organization
	if organization == "" {
		organization = existingContact.Organization
	}
	title := args.Title
	if title == "" {
		title = existingContact.Title
	}
	notes := args.Notes
	if notes == "" {
		notes = existingContact.Notes
	}

	// Build updated vCard
	card := buildVCard(args.UID, fullName, firstName, lastName, nickname,
		emails, phones, organization, title, notes)

	// Update the contact
	_, err = client.PutAddressObject(ctx, contactPath, card)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update contact: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Updated contact: %s", fullName)
	result.Contact = &Contact{
		UID:          args.UID,
		FullName:     fullName,
		FirstName:    firstName,
		LastName:     lastName,
		Nickname:     nickname,
		Emails:       emails,
		Phones:       phones,
		Organization: organization,
		Title:        title,
		Notes:        notes,
	}
	return result, nil
}

// deleteContactAction deletes a contact.
func deleteContactAction(ctx context.Context, client *carddav.Client, args CardDAVToolArgs) (*CardDAVToolResult, error) {
	result := &CardDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for delete_contact action"
		return result, nil
	}

	// Get address book path
	abPath, err := resolveAddressBookPath(ctx, client, args.AddressBook)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Delete the contact
	contactPath := abPath + args.UID + ".vcf"
	err = client.RemoveAll(ctx, contactPath)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to delete contact: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Deleted contact with UID: %s", args.UID)
	return result, nil
}

// Helper functions

func resolveAddressBookPath(ctx context.Context, client *carddav.Client, addressBookName string) (string, error) {
	// Find user principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to find user principal: %v", err)
	}

	// Find address book home
	homeSet, err := client.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("failed to find address book home: %v", err)
	}

	// If no specific address book requested, return first one (primary)
	if addressBookName == "" {
		addressBooks, err := client.FindAddressBooks(ctx, homeSet)
		if err != nil {
			return "", fmt.Errorf("failed to list address books: %v", err)
		}
		if len(addressBooks) == 0 {
			return "", fmt.Errorf("no address books found")
		}
		return addressBooks[0].Path, nil
	}

	// Search for named address book
	addressBooks, err := client.FindAddressBooks(ctx, homeSet)
	if err != nil {
		return "", fmt.Errorf("failed to list address books: %v", err)
	}

	for _, ab := range addressBooks {
		if ab.Name == addressBookName || ab.Path == addressBookName {
			return ab.Path, nil
		}
	}

	return "", fmt.Errorf("address book %q not found", addressBookName)
}

func parseAddressObject(obj carddav.AddressObject) *Contact {
	if obj.Card == nil {
		return nil
	}

	contact := &Contact{
		ETag: obj.ETag,
	}

	// Get UID
	contact.UID = obj.Card.Value(vcard.FieldUID)

	// Get formatted name
	contact.FullName = obj.Card.Value(vcard.FieldFormattedName)

	// Get structured name
	if name := obj.Card.Name(); name != nil {
		contact.FirstName = name.GivenName
		contact.LastName = name.FamilyName
	}

	// Get nickname
	contact.Nickname = obj.Card.Value(vcard.FieldNickname)

	// Get emails
	for _, field := range obj.Card[vcard.FieldEmail] {
		if field.Value != "" {
			contact.Emails = append(contact.Emails, field.Value)
		}
	}

	// Get phones
	for _, field := range obj.Card[vcard.FieldTelephone] {
		if field.Value != "" {
			contact.Phones = append(contact.Phones, field.Value)
		}
	}

	// Get organization
	contact.Organization = obj.Card.Value(vcard.FieldOrganization)

	// Get title
	contact.Title = obj.Card.Value(vcard.FieldTitle)

	// Get notes
	contact.Notes = obj.Card.Value(vcard.FieldNote)

	return contact
}

func buildVCard(uid, fullName, firstName, lastName, nickname string,
	emails, phones []string, organization, title, notes string) vcard.Card {
	card := make(vcard.Card)

	// Required: VERSION
	card.SetValue(vcard.FieldVersion, "4.0")

	// Required: UID
	card.SetValue(vcard.FieldUID, uid)

	// Required: FN (formatted name)
	card.SetValue(vcard.FieldFormattedName, fullName)

	// Optional: N (structured name)
	if firstName != "" || lastName != "" {
		name := &vcard.Name{
			FamilyName: lastName,
			GivenName:  firstName,
		}
		card.SetName(name)
	}

	// Optional: NICKNAME
	if nickname != "" {
		card.SetValue(vcard.FieldNickname, nickname)
	}

	// Optional: EMAIL(s)
	for _, email := range emails {
		if email != "" {
			card.AddValue(vcard.FieldEmail, email)
		}
	}

	// Optional: TEL(s)
	for _, phone := range phones {
		if phone != "" {
			card.AddValue(vcard.FieldTelephone, phone)
		}
	}

	// Optional: ORG
	if organization != "" {
		card.SetValue(vcard.FieldOrganization, organization)
	}

	// Optional: TITLE
	if title != "" {
		card.SetValue(vcard.FieldTitle, title)
	}

	// Optional: NOTE
	if notes != "" {
		card.SetValue(vcard.FieldNote, notes)
	}

	// Set revision timestamp
	card.SetRevision(time.Now())

	return card
}

// searchContactsAllFields searches contacts using a broader query.
// This is a fallback for servers that don't support advanced filtering.
func searchContactsAllFields(ctx context.Context, client *carddav.Client, abPath, query string, limit int) ([]carddav.AddressObject, error) {
	// Get all contacts and filter client-side
	allQuery := &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{
			Props: []string{
				vcard.FieldUID,
				vcard.FieldFormattedName,
				vcard.FieldName,
				vcard.FieldNickname,
				vcard.FieldEmail,
				vcard.FieldTelephone,
				vcard.FieldOrganization,
				vcard.FieldTitle,
				vcard.FieldNote,
			},
		},
	}

	objects, err := client.QueryAddressBook(ctx, abPath, allQuery)
	if err != nil {
		return nil, err
	}

	// Filter by query text (case-insensitive)
	queryLower := strings.ToLower(query)
	var matched []carddav.AddressObject
	for _, obj := range objects {
		contact := parseAddressObject(obj)
		if contact == nil {
			continue
		}

		// Check if query matches any field
		if strings.Contains(strings.ToLower(contact.FullName), queryLower) ||
			strings.Contains(strings.ToLower(contact.FirstName), queryLower) ||
			strings.Contains(strings.ToLower(contact.LastName), queryLower) ||
			strings.Contains(strings.ToLower(contact.Nickname), queryLower) ||
			strings.Contains(strings.ToLower(contact.Organization), queryLower) ||
			containsIgnoreCase(contact.Emails, query) ||
			containsIgnoreCase(contact.Phones, query) {
			matched = append(matched, obj)
			if len(matched) >= limit {
				break
			}
		}
	}

	return matched, nil
}

func containsIgnoreCase(slice []string, substr string) bool {
	substrLower := strings.ToLower(substr)
	for _, s := range slice {
		if strings.Contains(strings.ToLower(s), substrLower) {
			return true
		}
	}
	return false
}
