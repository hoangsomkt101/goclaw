package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PollOptions controls creation of a Zalo group poll.
type PollOptions struct {
	Question          string
	Options           []string
	ExpiredTime       int64
	AllowMultiChoices bool
	AllowAddOption    bool
	HideVotePreview   bool
	Anonymous         bool
}

// ReminderOptions controls creation of a Zalo reminder.
type ReminderOptions struct {
	Title     string
	Emoji     string
	StartTime int64
	Repeat    int
}

// GroupMemberInfo is the profile information needed for member discovery and mentions.
type GroupMemberInfo struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	ZaloName    string `json:"zalo_name,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
}

// encryptedRequest executes the common encrypted Zalo web API request shape.
// GET requests carry params in the query. POST requests carry a form body and may
// additionally mirror params in the query for the few endpoints that require it.
func encryptedRequest(ctx context.Context, sess *Session, service, path, method string, payload map[string]any, queryOnPost bool) (json.RawMessage, error) {
	baseURL := getServiceURL(sess, service)
	if baseURL == "" {
		return nil, fmt.Errorf("zalo_personal: no service URL for %s", service)
	}
	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: encrypt %s payload: %w", path, err)
	}

	query := map[string]any(nil)
	if method == http.MethodGet || queryOnPost {
		query = map[string]any{"params": encData}
	}
	reqURL := makeURL(sess, baseURL+path, query, true)
	var req *http.Request
	if method == http.MethodPost {
		req, err = http.NewRequestWithContext(ctx, method, reqURL, buildFormBody(map[string]string{"params": encData}))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, reqURL, nil)
	}
	if err != nil {
		return nil, err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return nil, fmt.Errorf("zalo_personal: parse %s response: %w", path, err)
	}
	if envelope.ErrorCode != 0 {
		return nil, fmt.Errorf("zalo_personal: %s error code %d: %s", path, envelope.ErrorCode, envelope.ErrorMessage)
	}
	if envelope.Data == nil {
		return json.RawMessage("null"), nil
	}
	plain, err := decryptDataField(sess, *envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: decrypt %s response: %w", path, err)
	}
	return json.RawMessage(plain), nil
}

func CreatePoll(ctx context.Context, sess *Session, groupID string, opts PollOptions) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "group", "/api/poll/create", http.MethodPost, map[string]any{
		"group_id": groupID, "question": opts.Question, "options": opts.Options,
		"expired_time": opts.ExpiredTime, "pinAct": false,
		"allow_multi_choices": opts.AllowMultiChoices, "allow_add_new_option": opts.AllowAddOption,
		"is_hide_vote_preview": opts.HideVotePreview, "is_anonymous": opts.Anonymous,
		"poll_type": 0, "src": 1, "imei": sess.IMEI,
	}, false)
}

func GetPollDetail(ctx context.Context, sess *Session, pollID int64) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "group", "/api/poll/detail", http.MethodPost,
		map[string]any{"poll_id": pollID, "imei": sess.IMEI}, false)
}

func LockPoll(ctx context.Context, sess *Session, pollID int64) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "group", "/api/poll/end", http.MethodPost,
		map[string]any{"poll_id": pollID, "imei": sess.IMEI}, false)
}

func CreateNote(ctx context.Context, sess *Session, groupID, title string, pin bool) (json.RawMessage, error) {
	params, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return nil, err
	}
	return encryptedRequest(ctx, sess, "group_board", "/api/board/topic/createv2", http.MethodPost, map[string]any{
		"grid": groupID, "type": 0, "color": -16777216, "emoji": "", "startTime": -1,
		"duration": -1, "params": string(params), "repeat": 0, "src": 1, "imei": sess.IMEI,
		"pinAct": boolInt(pin),
	}, false)
}

func CreateReminder(ctx context.Context, sess *Session, threadID string, threadType ThreadType, opts ReminderOptions) (json.RawMessage, error) {
	if opts.Emoji == "" {
		opts.Emoji = "⏰"
	}
	if opts.StartTime == 0 {
		opts.StartTime = time.Now().UnixMilli()
	}
	if threadType == ThreadTypeUser {
		objectData, err := json.Marshal(map[string]any{
			"toUid": threadID, "type": 0, "color": -16245706, "emoji": opts.Emoji,
			"startTime": opts.StartTime, "duration": -1, "params": map[string]string{"title": opts.Title},
			"needPin": false, "repeat": opts.Repeat, "creatorUid": sess.UID, "src": 1,
		})
		if err != nil {
			return nil, err
		}
		return encryptedRequest(ctx, sess, "group_board", "/api/board/oneone/create", http.MethodPost,
			map[string]any{"objectData": string(objectData), "imei": sess.IMEI}, false)
	}
	params, err := json.Marshal(map[string]string{"title": opts.Title})
	if err != nil {
		return nil, err
	}
	return encryptedRequest(ctx, sess, "group_board", "/api/board/topic/createv2", http.MethodPost, map[string]any{
		"grid": threadID, "type": 0, "color": -16245706, "emoji": opts.Emoji,
		"startTime": opts.StartTime, "duration": -1, "params": string(params), "repeat": opts.Repeat,
		"src": 1, "imei": sess.IMEI, "pinAct": 0,
	}, false)
}

func ListReminders(ctx context.Context, sess *Session, threadID string, threadType ThreadType, page, count int) (json.RawMessage, error) {
	if page <= 0 {
		page = 1
	}
	if count <= 0 || count > 100 {
		count = 20
	}
	object := map[string]any{"board_type": 1, "page": page, "count": count, "last_id": 0, "last_type": 0}
	path := "/api/board/oneone/list"
	if threadType == ThreadTypeGroup {
		object["group_id"] = threadID
		path = "/api/board/listReminder"
	} else {
		object["uid"] = threadID
	}
	objectData, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"objectData": string(objectData)}
	if threadType == ThreadTypeGroup {
		payload["imei"] = sess.IMEI
	}
	result, err := encryptedRequest(ctx, sess, "group_board", path, http.MethodGet, payload, false)
	if err != nil {
		return nil, err
	}
	var encoded string
	if json.Unmarshal(result, &encoded) == nil && json.Valid([]byte(encoded)) {
		return json.RawMessage(encoded), nil
	}
	return result, nil
}

func RemoveReminder(ctx context.Context, sess *Session, threadID string, threadType ThreadType, reminderID string) (json.RawMessage, error) {
	path := "/api/board/oneone/remove"
	payload := map[string]any{"uid": threadID, "reminderId": reminderID}
	if threadType == ThreadTypeGroup {
		path = "/api/board/topic/remove"
		payload = map[string]any{"grid": threadID, "topicId": reminderID, "imei": sess.IMEI}
	}
	return encryptedRequest(ctx, sess, "group_board", path, http.MethodPost, payload, false)
}

func UpdateGroupMembers(ctx context.Context, sess *Session, groupID string, memberIDs []string, add bool) (json.RawMessage, error) {
	path := "/api/group/kickout"
	payload := map[string]any{"grid": groupID, "members": memberIDs, "imei": sess.IMEI}
	if add {
		path = "/api/group/invite/v2"
		memberTypes := make([]int, len(memberIDs))
		for i := range memberTypes {
			memberTypes[i] = -1
		}
		payload["memberTypes"] = memberTypes
		payload["clientLang"] = sess.Language
	}
	return encryptedRequest(ctx, sess, "group", path, http.MethodPost, payload, false)
}

func UpdateGroupDeputies(ctx context.Context, sess *Session, groupID string, memberIDs []string, add bool) (json.RawMessage, error) {
	path := "/api/group/admins/remove"
	if add {
		path = "/api/group/admins/add"
	}
	return encryptedRequest(ctx, sess, "group", path, http.MethodGet,
		map[string]any{"grid": groupID, "members": memberIDs, "imei": sess.IMEI}, false)
}

func SendFriendRequest(ctx context.Context, sess *Session, userID, message string) (json.RawMessage, error) {
	srcParams, err := json.Marshal(map[string]string{"uidTo": userID})
	if err != nil {
		return nil, err
	}
	return encryptedRequest(ctx, sess, "friend", "/api/friend/sendreq", http.MethodPost, map[string]any{
		"toid": userID, "msg": message, "reqsrc": 30, "imei": sess.IMEI,
		"language": sess.Language, "srcParams": string(srcParams),
	}, false)
}

func AcceptFriendRequest(ctx context.Context, sess *Session, userID string) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "friend", "/api/friend/accept", http.MethodPost,
		map[string]any{"fid": userID, "language": sess.Language}, false)
}

func GetGroupLink(ctx context.Context, sess *Session, groupID string) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "group", "/api/group/link/detail", http.MethodGet,
		map[string]any{"grid": groupID, "imei": sess.IMEI}, false)
}

func SetGroupLink(ctx context.Context, sess *Session, groupID string, enabled bool) (json.RawMessage, error) {
	path := "/api/group/link/disable"
	payload := map[string]any{"grid": groupID}
	if enabled {
		path = "/api/group/link/new"
		payload["imei"] = sess.IMEI
	}
	return encryptedRequest(ctx, sess, "group", path, http.MethodGet, payload, false)
}

func JoinGroupLink(ctx context.Context, sess *Session, link string) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "group", "/api/group/link/join", http.MethodGet,
		map[string]any{"link": link, "clientLang": sess.Language}, false)
}

func SetPinnedConversation(ctx context.Context, sess *Session, threadID string, threadType ThreadType, pinned bool) (json.RawMessage, error) {
	prefix := "u"
	if threadType == ThreadTypeGroup {
		prefix = "g"
	}
	action := 2
	if pinned {
		action = 1
	}
	return encryptedRequest(ctx, sess, "conversation", "/api/pinconvers/updatev2", http.MethodPost,
		map[string]any{"actionType": action, "conversations": []string{prefix + threadID}}, false)
}

func SetMute(ctx context.Context, sess *Session, threadID string, threadType ThreadType, muted bool, duration int64) (json.RawMessage, error) {
	action, muteType := 3, 1
	if muted {
		action = 1
	}
	if threadType == ThreadTypeGroup {
		muteType = 2
	}
	if !muted || duration == 0 {
		duration = -1
	}
	return encryptedRequest(ctx, sess, "profile", "/api/social/profile/setmute", http.MethodPost, map[string]any{
		"toid": threadID, "duration": duration, "action": action, "startTime": time.Now().Unix(),
		"muteType": muteType, "imei": sess.IMEI,
	}, false)
}

func UpdateProfileBio(ctx context.Context, sess *Session, bio string) (json.RawMessage, error) {
	return encryptedRequest(ctx, sess, "profile", "/api/social/profile/status", http.MethodPost,
		map[string]any{"status": bio}, true)
}

func GetUserInfo(ctx context.Context, sess *Session, userIDs []string, avatarSize int) (json.RawMessage, error) {
	if avatarSize == 0 {
		avatarSize = 120
	}
	versioned := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if strings.Contains(id, "_") {
			versioned = append(versioned, id)
		} else {
			versioned = append(versioned, id+"_0")
		}
	}
	return encryptedRequest(ctx, sess, "profile", "/api/social/friend/getprofiles/v2", http.MethodPost, map[string]any{
		"phonebook_version": sess.ExtraVer.Phonebook, "friend_pversion_map": versioned,
		"avatar_size": avatarSize, "language": sess.Language, "show_online_status": 1, "imei": sess.IMEI,
	}, false)
}

func UpdateProfile(ctx context.Context, sess *Session, name, dateOfBirth string, gender int) (json.RawMessage, error) {
	profile, err := json.Marshal(map[string]any{"name": name, "dob": dateOfBirth, "gender": gender})
	if err != nil {
		return nil, err
	}
	business, err := json.Marshal(map[string]any{})
	if err != nil {
		return nil, err
	}
	return encryptedRequest(ctx, sess, "profile", "/api/social/profile/update", http.MethodPost,
		map[string]any{"profile": string(profile), "biz": string(business), "language": sess.Language}, false)
}

func UpdateActiveStatus(ctx context.Context, sess *Session, active bool) (json.RawMessage, error) {
	path := "/api/social/profile/deactive"
	if active {
		path = "/api/social/profile/ping"
	}
	return encryptedRequest(ctx, sess, "profile", path, http.MethodGet,
		map[string]any{"status": boolInt(active), "imei": sess.IMEI}, false)
}

// AddReaction adds one of Zalo's standard reactions to a message.
func AddReaction(ctx context.Context, sess *Session, threadID string, threadType ThreadType, messageID, clientMessageID, icon string) (json.RawMessage, error) {
	type reactionCode struct {
		Type, Source int
		Icon         string
	}
	codes := map[string]reactionCode{
		"LIKE": {3, 6, "/-strong"}, "HEART": {5, 6, "/-heart"}, "HAHA": {0, 6, ":>"}, "WOW": {32, 6, ":o"},
		"CRY": {2, 6, ":-(("}, "ANGRY": {20, 6, ":-h"}, "DISLIKE": {4, 6, "/-weak"}, "LOVE": {29, 6, ";xx"},
	}
	code, ok := codes[strings.ToUpper(icon)]
	if !ok {
		return nil, fmt.Errorf("zalo_personal: unsupported reaction %q", icon)
	}
	globalMessageID, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: invalid message_id: %w", err)
	}
	clientID, err := strconv.ParseInt(clientMessageID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("zalo_personal: invalid client_message_id: %w", err)
	}
	message, err := json.Marshal(map[string]any{
		"rMsg":  []map[string]any{{"gMsgID": globalMessageID, "cMsgID": clientID, "msgType": 1}},
		"rIcon": code.Icon, "rType": code.Type, "source": code.Source,
	})
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"react_list": []map[string]any{{"message": string(message), "clientId": time.Now().UnixMilli()}}}
	path := "/api/message/reaction"
	if threadType == ThreadTypeGroup {
		path = "/api/group/reaction"
		payload["grid"] = threadID
		payload["imei"] = sess.IMEI
	} else {
		payload["toid"] = threadID
	}
	return encryptedRequest(ctx, sess, "reaction", path, http.MethodPost, payload, false)
}

// FetchGroupMembers retrieves the full member ID list and resolves display profiles.
func FetchGroupMembers(ctx context.Context, sess *Session, groupID string) ([]GroupMemberInfo, error) {
	gridVerMap, err := json.Marshal(map[string]int{groupID: 0})
	if err != nil {
		return nil, err
	}
	raw, err := encryptedRequest(ctx, sess, "group", "/api/group/getmg-v2", http.MethodPost,
		map[string]any{"gridVerMap": string(gridVerMap)}, false)
	if err != nil {
		return nil, err
	}
	var groupResult struct {
		GridInfoMap map[string]struct {
			MemberIDs   []string `json:"memberIds"`
			CurrentMems []struct {
				ID       string `json:"id"`
				DName    string `json:"dName"`
				ZaloName string `json:"zaloName"`
				Avatar   string `json:"avatar"`
			} `json:"currentMems"`
		} `json:"gridInfoMap"`
	}
	if err := json.Unmarshal(raw, &groupResult); err != nil {
		return nil, fmt.Errorf("zalo_personal: parse group members: %w", err)
	}
	info, ok := groupResult.GridInfoMap[groupID]
	if !ok {
		return nil, fmt.Errorf("zalo_personal: group %q was not returned", groupID)
	}
	fallback := make(map[string]GroupMemberInfo, len(info.CurrentMems))
	for _, member := range info.CurrentMems {
		fallback[member.ID] = GroupMemberInfo{UserID: member.ID, DisplayName: member.DName, ZaloName: member.ZaloName, Avatar: member.Avatar}
	}
	memberIDs := info.MemberIDs
	if len(memberIDs) == 0 {
		for id := range fallback {
			memberIDs = append(memberIDs, id)
		}
	}

	profiles := make(map[string]GroupMemberInfo, len(memberIDs))
	const batchSize = 100
	for start := 0; start < len(memberIDs); start += batchSize {
		end := min(start+batchSize, len(memberIDs))
		versioned := make([]string, 0, end-start)
		for _, id := range memberIDs[start:end] {
			if strings.HasSuffix(id, "_0") {
				versioned = append(versioned, id)
			} else {
				versioned = append(versioned, id+"_0")
			}
		}
		profileRaw, reqErr := encryptedRequest(ctx, sess, "profile", "/api/social/group/members", http.MethodGet,
			map[string]any{"friend_pversion_map": versioned}, false)
		if reqErr != nil {
			return nil, reqErr
		}
		var profileResult struct {
			Profiles map[string]struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
				ZaloName    string `json:"zaloName"`
				Avatar      string `json:"avatar"`
			} `json:"profiles"`
		}
		if err := json.Unmarshal(profileRaw, &profileResult); err != nil {
			return nil, fmt.Errorf("zalo_personal: parse member profiles: %w", err)
		}
		for key, profile := range profileResult.Profiles {
			id := profile.ID
			if id == "" {
				id = strings.TrimSuffix(key, "_0")
			}
			profiles[id] = GroupMemberInfo{UserID: id, DisplayName: profile.DisplayName, ZaloName: profile.ZaloName, Avatar: profile.Avatar}
		}
	}

	members := make([]GroupMemberInfo, 0, len(memberIDs))
	for _, id := range memberIDs {
		member, ok := profiles[id]
		if !ok {
			member = fallback[id]
		}
		if member.UserID == "" {
			member.UserID = id
		}
		if member.DisplayName == "" {
			member.DisplayName = member.ZaloName
		}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].DisplayName < members[j].DisplayName })
	return members, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
