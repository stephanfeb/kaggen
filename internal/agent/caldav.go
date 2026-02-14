package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/oauth"
)

const (
	caldavDefaultLimit   = 25
	caldavMaxLimit       = 100
	caldavDefaultDays    = 30
	caldavMaxResponseLen = 50 * 1024 // 50KB max for event bodies
)

// CalDAVToolArgs defines the input arguments for the caldav tool.
type CalDAVToolArgs struct {
	// Required fields
	Action   string `json:"action" jsonschema:"required,description=Action to perform: list_calendars list_events query_events get_event create_event update_event delete_event free_busy,enum=list_calendars,enum=list_events,enum=query_events,enum=get_event,enum=create_event,enum=update_event,enum=delete_event,enum=free_busy"`
	Provider string `json:"provider" jsonschema:"required,description=Provider name (e.g. google icloud fastmail) for server configuration and auth"`

	// Authentication
	Email string `json:"email,omitempty" jsonschema:"description=Email/username for authentication. Required for OAuth providers"`

	// Calendar selection
	Calendar string `json:"calendar,omitempty" jsonschema:"description=Calendar name or path. Default: primary calendar"`

	// Event identification (for get/update/delete)
	UID string `json:"uid,omitempty" jsonschema:"description=Event UID. Required for get_event update_event and delete_event"`

	// Event creation/update fields
	Summary     string   `json:"summary,omitempty" jsonschema:"description=Event title/summary. Required for create_event"`
	Description string   `json:"description,omitempty" jsonschema:"description=Event description/notes"`
	Location    string   `json:"location,omitempty" jsonschema:"description=Event location"`
	Start       string   `json:"start,omitempty" jsonschema:"description=Start datetime in RFC3339 format (e.g. 2024-01-15T14:00:00Z). Required for create_event and free_busy"`
	End         string   `json:"end,omitempty" jsonschema:"description=End datetime in RFC3339 format. Required for create_event and free_busy"`
	AllDay      bool     `json:"all_day,omitempty" jsonschema:"description=If true treat start/end as dates not datetimes"`
	Attendees   []string `json:"attendees,omitempty" jsonschema:"description=List of attendee email addresses"`

	// Query/filter fields
	TimeMin string `json:"time_min,omitempty" jsonschema:"description=Lower bound for event time range (RFC3339). Default: now"`
	TimeMax string `json:"time_max,omitempty" jsonschema:"description=Upper bound for event time range (RFC3339). Default: +30 days"`
	Query   string `json:"query,omitempty" jsonschema:"description=Text search query for event summaries"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Maximum events to return (default: 25 max: 100)"`
}

// CalDAVToolResult is the result of a CalDAV operation.
type CalDAVToolResult struct {
	Success   bool             `json:"success"`
	Message   string           `json:"message"`
	Event     *CalendarEvent   `json:"event,omitempty"`     // For get_event, create_event, update_event
	Events    []CalendarEvent  `json:"events,omitempty"`    // For list_events, query_events
	Calendars []CalendarInfo   `json:"calendars,omitempty"` // For list_calendars
	FreeBusy  []FreeBusyPeriod `json:"free_busy,omitempty"` // For free_busy
}

// CalendarEvent represents a calendar event.
type CalendarEvent struct {
	UID         string   `json:"uid"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	Start       string   `json:"start"`            // RFC3339
	End         string   `json:"end"`              // RFC3339
	AllDay      bool     `json:"all_day,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	Status      string   `json:"status,omitempty"`     // CONFIRMED, TENTATIVE, CANCELLED
	Organizer   string   `json:"organizer,omitempty"`
	ETag        string   `json:"etag,omitempty"`       // For conflict detection
	CalendarURL string   `json:"calendar_url,omitempty"`
}

// CalendarInfo describes an available calendar.
type CalendarInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

// FreeBusyPeriod represents a busy time slot.
type FreeBusyPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Type  string `json:"type"` // BUSY, BUSY-TENTATIVE, BUSY-UNAVAILABLE
}

// NewCalDAVTool creates a CalDAV tool with OAuth and Basic auth support.
func NewCalDAVTool(
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
		func(ctx context.Context, args CalDAVToolArgs) (*CalDAVToolResult, error) {
			return executeCalDAVTool(ctx, args, userID, allowed, secrets, tokenGetter, providerGetter)
		},
		function.WithName("caldav"),
		function.WithDescription("Manage calendar events via CalDAV. Actions: list_calendars, list_events, query_events, get_event, create_event, update_event, delete_event, free_busy. Requires OAuth authorization or Basic auth credentials."),
	)
}

func executeCalDAVTool(
	ctx context.Context,
	args CalDAVToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter DAVTokenGetter,
	providerGetter DAVProviderGetter,
) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

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
	clientCfg, err := buildCalDAVClientConfig(ctx, args, userID, provider, secrets, tokenGetter)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Create CalDAV client
	httpClient := NewDAVHTTPClient(clientCfg)
	client, err := caldav.NewClient(httpClient, clientCfg.ServerURL)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create CalDAV client: %v", err)
		return result, nil
	}

	// Execute action
	switch args.Action {
	case "list_calendars":
		return listCalendarsAction(ctx, client, args)
	case "list_events":
		return listEventsAction(ctx, client, args)
	case "query_events":
		return queryEventsAction(ctx, client, args)
	case "get_event":
		return getEventAction(ctx, client, args)
	case "create_event":
		return createEventAction(ctx, client, args)
	case "update_event":
		return updateEventAction(ctx, client, args)
	case "delete_event":
		return deleteEventAction(ctx, client, args)
	case "free_busy":
		return freeBusyAction(ctx, client, args)
	default:
		result.Message = fmt.Sprintf("Unknown action %q", args.Action)
		return result, nil
	}
}

func buildCalDAVClientConfig(
	ctx context.Context,
	args CalDAVToolArgs,
	userID string,
	provider config.OAuthProvider,
	secrets map[string]string,
	tokenGetter DAVTokenGetter,
) (DAVClientConfig, error) {
	cfg := DAVClientConfig{}

	// Resolve server URL
	serverURL, err := ResolveDAVServerURL(ctx, provider, args.Email, "caldav")
	if err != nil {
		return cfg, fmt.Errorf("failed to resolve CalDAV server: %v", err)
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
	usernameKey := args.Provider + "-caldav-username"
	passwordKey := args.Provider + "-caldav-password"
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

// listCalendarsAction lists available calendars.
func listCalendarsAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	// Find user principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to find user principal: %v", err)
		return result, nil
	}

	// Find calendar home
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to find calendar home: %v", err)
		return result, nil
	}

	// List calendars
	calendars, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to list calendars: %v", err)
		return result, nil
	}

	result.Calendars = make([]CalendarInfo, 0, len(calendars))
	for _, cal := range calendars {
		info := CalendarInfo{
			Name: cal.Name,
			Path: cal.Path,
		}
		if cal.Description != "" {
			info.Description = cal.Description
		}
		result.Calendars = append(result.Calendars, info)
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d calendars", len(result.Calendars))
	return result, nil
}

// listEventsAction lists events in a time range.
func listEventsAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Parse time range
	timeMin, timeMax := parseTimeRange(args.TimeMin, args.TimeMax)

	// Set limit
	limit := args.Limit
	if limit <= 0 {
		limit = caldavDefaultLimit
	}
	if limit > caldavMaxLimit {
		limit = caldavMaxLimit
	}

	// Query events
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name: "VCALENDAR",
			Comps: []caldav.CalendarCompRequest{
				{Name: "VEVENT"},
			},
		},
		CompFilter: caldav.CompFilter{
			Name: "VCALENDAR",
			Comps: []caldav.CompFilter{
				{
					Name:  "VEVENT",
					Start: timeMin,
					End:   timeMax,
				},
			},
		},
	}

	objects, err := client.QueryCalendar(ctx, calPath, query)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to query events: %v", err)
		return result, nil
	}

	result.Events = make([]CalendarEvent, 0, len(objects))
	for i, obj := range objects {
		if i >= limit {
			break
		}
		event := parseCalendarObject(obj)
		if event != nil {
			result.Events = append(result.Events, *event)
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d events", len(result.Events))
	return result, nil
}

// queryEventsAction searches events by text.
func queryEventsAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	if args.Query == "" {
		result.Message = "Error: 'query' is required for query_events action"
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Parse time range (default to wider range for search)
	var timeMin, timeMax time.Time
	if args.TimeMin != "" {
		timeMin, _ = time.Parse(time.RFC3339, args.TimeMin)
	} else {
		timeMin = time.Now().AddDate(-1, 0, 0) // 1 year ago
	}
	if args.TimeMax != "" {
		timeMax, _ = time.Parse(time.RFC3339, args.TimeMax)
	} else {
		timeMax = time.Now().AddDate(1, 0, 0) // 1 year ahead
	}

	// Set limit
	limit := args.Limit
	if limit <= 0 {
		limit = caldavDefaultLimit
	}
	if limit > caldavMaxLimit {
		limit = caldavMaxLimit
	}

	// Query all events in time range
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name: "VCALENDAR",
			Comps: []caldav.CalendarCompRequest{
				{Name: "VEVENT"},
			},
		},
		CompFilter: caldav.CompFilter{
			Name: "VCALENDAR",
			Comps: []caldav.CompFilter{
				{
					Name:  "VEVENT",
					Start: timeMin,
					End:   timeMax,
				},
			},
		},
	}

	objects, err := client.QueryCalendar(ctx, calPath, query)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to query events: %v", err)
		return result, nil
	}

	// Filter by query text (case-insensitive)
	queryLower := strings.ToLower(args.Query)
	result.Events = make([]CalendarEvent, 0)
	for _, obj := range objects {
		event := parseCalendarObject(obj)
		if event != nil {
			// Check if query matches summary or description
			if strings.Contains(strings.ToLower(event.Summary), queryLower) ||
				strings.Contains(strings.ToLower(event.Description), queryLower) {
				result.Events = append(result.Events, *event)
				if len(result.Events) >= limit {
					break
				}
			}
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d events matching '%s'", len(result.Events), args.Query)
	return result, nil
}

// getEventAction retrieves a single event by UID.
func getEventAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for get_event action"
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Get the event
	objects, err := client.MultiGetCalendar(ctx, calPath, &caldav.CalendarMultiGet{
		Paths: []string{calPath + args.UID + ".ics"},
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get event: %v", err)
		return result, nil
	}

	if len(objects) == 0 {
		result.Message = fmt.Sprintf("Event with UID %q not found", args.UID)
		return result, nil
	}

	event := parseCalendarObject(objects[0])
	if event == nil {
		result.Message = "Failed to parse event data"
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Retrieved event: %s", event.Summary)
	result.Event = event
	return result, nil
}

// createEventAction creates a new calendar event.
func createEventAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	// Validate required fields
	if args.Summary == "" {
		result.Message = "Error: 'summary' is required for create_event action"
		return result, nil
	}
	if args.Start == "" {
		result.Message = "Error: 'start' is required for create_event action"
		return result, nil
	}
	if args.End == "" {
		result.Message = "Error: 'end' is required for create_event action"
		return result, nil
	}

	// Parse times
	startTime, err := time.Parse(time.RFC3339, args.Start)
	if err != nil {
		result.Message = fmt.Sprintf("Invalid start time format: %v", err)
		return result, nil
	}
	endTime, err := time.Parse(time.RFC3339, args.End)
	if err != nil {
		result.Message = fmt.Sprintf("Invalid end time format: %v", err)
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Generate UID if not provided
	uid := args.UID
	if uid == "" {
		uid = fmt.Sprintf("%d-%s@kaggen", time.Now().UnixNano(), generateRandomString(8))
	}

	// Build iCalendar data
	icalData := buildICalEvent(uid, args.Summary, args.Description, args.Location, startTime, endTime, args.Attendees, args.AllDay)

	// Create the event
	eventPath := calPath + uid + ".ics"
	obj := caldav.CalendarObject{
		Path: eventPath,
		Data: icalData,
	}

	_, err = client.PutCalendarObject(ctx, eventPath, icalData)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create event: %v", err)
		return result, nil
	}

	event := parseCalendarObject(obj)
	result.Success = true
	result.Message = fmt.Sprintf("Created event: %s", args.Summary)
	result.Event = event
	return result, nil
}

// updateEventAction updates an existing event.
func updateEventAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for update_event action"
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// First, get the existing event
	eventPath := calPath + args.UID + ".ics"
	objects, err := client.MultiGetCalendar(ctx, calPath, &caldav.CalendarMultiGet{
		Paths: []string{eventPath},
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get existing event: %v", err)
		return result, nil
	}

	if len(objects) == 0 {
		result.Message = fmt.Sprintf("Event with UID %q not found", args.UID)
		return result, nil
	}

	existingEvent := parseCalendarObject(objects[0])
	if existingEvent == nil {
		result.Message = "Failed to parse existing event"
		return result, nil
	}

	// Merge updates
	summary := args.Summary
	if summary == "" {
		summary = existingEvent.Summary
	}
	description := args.Description
	if description == "" {
		description = existingEvent.Description
	}
	location := args.Location
	if location == "" {
		location = existingEvent.Location
	}

	// Parse times (use existing if not provided)
	var startTime, endTime time.Time
	if args.Start != "" {
		startTime, err = time.Parse(time.RFC3339, args.Start)
		if err != nil {
			result.Message = fmt.Sprintf("Invalid start time format: %v", err)
			return result, nil
		}
	} else {
		startTime, _ = time.Parse(time.RFC3339, existingEvent.Start)
	}
	if args.End != "" {
		endTime, err = time.Parse(time.RFC3339, args.End)
		if err != nil {
			result.Message = fmt.Sprintf("Invalid end time format: %v", err)
			return result, nil
		}
	} else {
		endTime, _ = time.Parse(time.RFC3339, existingEvent.End)
	}

	attendees := args.Attendees
	if len(attendees) == 0 {
		attendees = existingEvent.Attendees
	}

	// Build updated iCalendar data
	icalData := buildICalEvent(args.UID, summary, description, location, startTime, endTime, attendees, args.AllDay)

	// Update the event
	_, err = client.PutCalendarObject(ctx, eventPath, icalData)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update event: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Updated event: %s", summary)
	result.Event = &CalendarEvent{
		UID:         args.UID,
		Summary:     summary,
		Description: description,
		Location:    location,
		Start:       startTime.Format(time.RFC3339),
		End:         endTime.Format(time.RFC3339),
		Attendees:   attendees,
	}
	return result, nil
}

// deleteEventAction deletes an event.
func deleteEventAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	if args.UID == "" {
		result.Message = "Error: 'uid' is required for delete_event action"
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Delete the event
	eventPath := calPath + args.UID + ".ics"
	err = client.RemoveAll(ctx, eventPath)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to delete event: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Deleted event with UID: %s", args.UID)
	return result, nil
}

// freeBusyAction queries free/busy information.
func freeBusyAction(ctx context.Context, client *caldav.Client, args CalDAVToolArgs) (*CalDAVToolResult, error) {
	result := &CalDAVToolResult{}

	if args.Start == "" {
		result.Message = "Error: 'start' is required for free_busy action"
		return result, nil
	}
	if args.End == "" {
		result.Message = "Error: 'end' is required for free_busy action"
		return result, nil
	}

	// Parse times
	startTime, err := time.Parse(time.RFC3339, args.Start)
	if err != nil {
		result.Message = fmt.Sprintf("Invalid start time format: %v", err)
		return result, nil
	}
	endTime, err := time.Parse(time.RFC3339, args.End)
	if err != nil {
		result.Message = fmt.Sprintf("Invalid end time format: %v", err)
		return result, nil
	}

	// Get calendar path
	calPath, err := resolveCalendarPath(ctx, client, args.Calendar)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Query events in the time range
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name: "VCALENDAR",
			Comps: []caldav.CalendarCompRequest{
				{Name: "VEVENT"},
			},
		},
		CompFilter: caldav.CompFilter{
			Name: "VCALENDAR",
			Comps: []caldav.CompFilter{
				{
					Name:  "VEVENT",
					Start: startTime,
					End:   endTime,
				},
			},
		},
	}

	objects, err := client.QueryCalendar(ctx, calPath, query)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to query free/busy: %v", err)
		return result, nil
	}

	// Convert events to free/busy periods
	result.FreeBusy = make([]FreeBusyPeriod, 0, len(objects))
	for _, obj := range objects {
		event := parseCalendarObject(obj)
		if event != nil {
			busyType := "BUSY"
			if event.Status == "TENTATIVE" {
				busyType = "BUSY-TENTATIVE"
			} else if event.Status == "CANCELLED" {
				continue // Skip cancelled events
			}
			result.FreeBusy = append(result.FreeBusy, FreeBusyPeriod{
				Start: event.Start,
				End:   event.End,
				Type:  busyType,
			})
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d busy periods", len(result.FreeBusy))
	return result, nil
}

// Helper functions

func resolveCalendarPath(ctx context.Context, client *caldav.Client, calendarName string) (string, error) {
	// Find user principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to find user principal: %v", err)
	}

	// Find calendar home
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("failed to find calendar home: %v", err)
	}

	// If no specific calendar requested, return home set
	if calendarName == "" {
		// List calendars and return the first one (primary)
		calendars, err := client.FindCalendars(ctx, homeSet)
		if err != nil {
			return "", fmt.Errorf("failed to list calendars: %v", err)
		}
		if len(calendars) == 0 {
			return "", fmt.Errorf("no calendars found")
		}
		return calendars[0].Path, nil
	}

	// Search for named calendar
	calendars, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		return "", fmt.Errorf("failed to list calendars: %v", err)
	}

	for _, cal := range calendars {
		if cal.Name == calendarName || cal.Path == calendarName {
			return cal.Path, nil
		}
	}

	return "", fmt.Errorf("calendar %q not found", calendarName)
}

func parseTimeRange(timeMinStr, timeMaxStr string) (time.Time, time.Time) {
	var timeMin, timeMax time.Time

	if timeMinStr != "" {
		timeMin, _ = time.Parse(time.RFC3339, timeMinStr)
	}
	if timeMin.IsZero() {
		timeMin = time.Now()
	}

	if timeMaxStr != "" {
		timeMax, _ = time.Parse(time.RFC3339, timeMaxStr)
	}
	if timeMax.IsZero() {
		timeMax = timeMin.AddDate(0, 0, caldavDefaultDays)
	}

	return timeMin, timeMax
}

func parseCalendarObject(obj caldav.CalendarObject) *CalendarEvent {
	if obj.Data == nil {
		return nil
	}

	event := &CalendarEvent{
		CalendarURL: obj.Path,
		ETag:        obj.ETag,
	}

	// Parse iCalendar data
	data := obj.Data
	for _, comp := range data.Children {
		if comp.Name != "VEVENT" {
			continue
		}

		for _, prop := range comp.Props.Values("UID") {
			event.UID = prop.Value
		}
		for _, prop := range comp.Props.Values("SUMMARY") {
			event.Summary = prop.Value
		}
		for _, prop := range comp.Props.Values("DESCRIPTION") {
			event.Description = prop.Value
		}
		for _, prop := range comp.Props.Values("LOCATION") {
			event.Location = prop.Value
		}
		for _, prop := range comp.Props.Values("STATUS") {
			event.Status = prop.Value
		}
		for _, prop := range comp.Props.Values("ORGANIZER") {
			event.Organizer = prop.Value
		}
		for _, prop := range comp.Props.Values("DTSTART") {
			if t, err := prop.DateTime(nil); err == nil {
				event.Start = t.Format(time.RFC3339)
			}
		}
		for _, prop := range comp.Props.Values("DTEND") {
			if t, err := prop.DateTime(nil); err == nil {
				event.End = t.Format(time.RFC3339)
			}
		}
		for _, prop := range comp.Props.Values("ATTENDEE") {
			email := strings.TrimPrefix(prop.Value, "mailto:")
			event.Attendees = append(event.Attendees, email)
		}

		break // Only process first VEVENT
	}

	return event
}

func buildICalEvent(uid, summary, description, location string, start, end time.Time, attendees []string, allDay bool) *ical.Calendar {
	// Build a minimal iCalendar object
	cal := ical.NewCalendar()

	// Add VCALENDAR properties
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Kaggen//CalDAV Tool//EN")

	// Create VEVENT component
	vevent := ical.NewEvent()
	vevent.Props.SetText(ical.PropUID, uid)
	vevent.Props.SetText(ical.PropSummary, summary)
	if description != "" {
		vevent.Props.SetText(ical.PropDescription, description)
	}
	if location != "" {
		vevent.Props.SetText(ical.PropLocation, location)
	}

	// Set timestamps
	vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

	if allDay {
		// For all-day events, use DATE format
		vevent.Props.SetDate(ical.PropDateTimeStart, start)
		vevent.Props.SetDate(ical.PropDateTimeEnd, end)
	} else {
		vevent.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
		vevent.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
	}

	// Add attendees
	for _, email := range attendees {
		attendeeProp := ical.NewProp(ical.PropAttendee)
		attendeeProp.Value = "mailto:" + email
		vevent.Props.Add(attendeeProp)
	}

	cal.Children = append(cal.Children, vevent.Component)
	return cal
}

func generateRandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
