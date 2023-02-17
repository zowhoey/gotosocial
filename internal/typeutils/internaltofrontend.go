/*
   GoToSocial
   Copyright (C) 2021-2023 GoToSocial Authors admin@gotosocial.org

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package typeutils

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/log"
	"github.com/superseriousbusiness/gotosocial/internal/media"
	"github.com/superseriousbusiness/gotosocial/internal/util"
)

const (
	instanceStatusesCharactersReservedPerURL    = 25
	instanceMediaAttachmentsImageMatrixLimit    = 16777216 // width * height
	instanceMediaAttachmentsVideoMatrixLimit    = 16777216 // width * height
	instanceMediaAttachmentsVideoFrameRateLimit = 60
	instancePollsMinExpiration                  = 300     // seconds
	instancePollsMaxExpiration                  = 2629746 // seconds
	instanceAccountsMaxFeaturedTags             = 10
	instanceSourceURL                           = "https://github.com/superseriousbusiness/gotosocial"
)

func (c *converter) AccountToAPIAccountSensitive(ctx context.Context, a *gtsmodel.Account) (*apimodel.Account, error) {
	// we can build this sensitive account easily by first getting the public account....
	apiAccount, err := c.AccountToAPIAccountPublic(ctx, a)
	if err != nil {
		return nil, err
	}

	// then adding the Source object to it...

	// check pending follow requests aimed at this account
	frs, err := c.db.GetAccountFollowRequests(ctx, a.ID)
	if err != nil {
		if err != db.ErrNoEntries {
			return nil, fmt.Errorf("error getting follow requests: %s", err)
		}
	}
	var frc int
	if frs != nil {
		frc = len(frs)
	}

	statusFormat := string(apimodel.StatusFormatDefault)
	if a.StatusFormat != "" {
		statusFormat = a.StatusFormat
	}

	apiAccount.Source = &apimodel.Source{
		Privacy:             c.VisToAPIVis(ctx, a.Privacy),
		Sensitive:           *a.Sensitive,
		Language:            a.Language,
		StatusFormat:        statusFormat,
		Note:                a.NoteRaw,
		Fields:              apiAccount.Fields,
		FollowRequestsCount: frc,
	}

	return apiAccount, nil
}

func (c *converter) AccountToAPIAccountPublic(ctx context.Context, a *gtsmodel.Account) (*apimodel.Account, error) {
	// count followers
	followersCount, err := c.db.CountAccountFollowedBy(ctx, a.ID, false)
	if err != nil {
		return nil, fmt.Errorf("error counting followers: %s", err)
	}

	// count following
	followingCount, err := c.db.CountAccountFollows(ctx, a.ID, false)
	if err != nil {
		return nil, fmt.Errorf("error counting following: %s", err)
	}

	// count statuses
	statusesCount, err := c.db.CountAccountStatuses(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("error counting statuses: %s", err)
	}

	// check when the last status was
	var lastStatusAt *string
	lastPosted, err := c.db.GetAccountLastPosted(ctx, a.ID, false)
	if err == nil && !lastPosted.IsZero() {
		lastStatusAtTemp := util.FormatISO8601(lastPosted)
		lastStatusAt = &lastStatusAtTemp
	}

	// set account avatar fields if available
	var aviURL string
	var aviURLStatic string
	if a.AvatarMediaAttachmentID != "" {
		if a.AvatarMediaAttachment == nil {
			avi, err := c.db.GetAttachmentByID(ctx, a.AvatarMediaAttachmentID)
			if err != nil {
				log.Errorf(ctx, "error getting Avatar with id %s: %s", a.AvatarMediaAttachmentID, err)
			}
			a.AvatarMediaAttachment = avi
		}
		if a.AvatarMediaAttachment != nil {
			aviURL = a.AvatarMediaAttachment.URL
			aviURLStatic = a.AvatarMediaAttachment.Thumbnail.URL
		}
	}

	// set account header fields if available
	var headerURL string
	var headerURLStatic string
	if a.HeaderMediaAttachmentID != "" {
		if a.HeaderMediaAttachment == nil {
			avi, err := c.db.GetAttachmentByID(ctx, a.HeaderMediaAttachmentID)
			if err != nil {
				log.Errorf(ctx, "error getting Header with id %s: %s", a.HeaderMediaAttachmentID, err)
			}
			a.HeaderMediaAttachment = avi
		}
		if a.HeaderMediaAttachment != nil {
			headerURL = a.HeaderMediaAttachment.URL
			headerURLStatic = a.HeaderMediaAttachment.Thumbnail.URL
		}
	}

	// preallocate frontend fields slice
	fields := make([]apimodel.Field, len(a.Fields))

	// Convert account GTS model fields to frontend
	for i, field := range a.Fields {
		mField := apimodel.Field{
			Name:  field.Name,
			Value: field.Value,
		}
		if !field.VerifiedAt.IsZero() {
			mField.VerifiedAt = util.FormatISO8601(field.VerifiedAt)
		}
		fields[i] = mField
	}

	// convert account gts model emojis to frontend api model emojis
	apiEmojis, err := c.convertEmojisToAPIEmojis(ctx, a.Emojis, a.EmojiIDs)
	if err != nil {
		log.Errorf(ctx, "error converting account emojis: %v", err)
	}

	var acct string
	var role *apimodel.AccountRole

	if a.Domain != "" {
		// this is a remote user
		acct = a.Username + "@" + a.Domain
	} else {
		// this is a local user
		acct = a.Username
		user, err := c.db.GetUserByAccountID(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("AccountToAPIAccountPublic: error getting user from database for account id %s: %s", a.ID, err)
		}

		switch {
		case *user.Admin:
			role = &apimodel.AccountRole{Name: apimodel.AccountRoleAdmin}
		case *user.Moderator:
			role = &apimodel.AccountRole{Name: apimodel.AccountRoleModerator}
		default:
			role = &apimodel.AccountRole{Name: apimodel.AccountRoleUser}
		}
	}

	var suspended bool
	if !a.SuspendedAt.IsZero() {
		suspended = true
	}

	accountFrontend := &apimodel.Account{
		ID:             a.ID,
		Username:       a.Username,
		Acct:           acct,
		DisplayName:    a.DisplayName,
		Locked:         *a.Locked,
		Discoverable:   *a.Discoverable,
		Bot:            *a.Bot,
		CreatedAt:      util.FormatISO8601(a.CreatedAt),
		Note:           a.Note,
		URL:            a.URL,
		Avatar:         aviURL,
		AvatarStatic:   aviURLStatic,
		Header:         headerURL,
		HeaderStatic:   headerURLStatic,
		FollowersCount: followersCount,
		FollowingCount: followingCount,
		StatusesCount:  statusesCount,
		LastStatusAt:   lastStatusAt,
		Emojis:         apiEmojis,
		Fields:         fields,
		Suspended:      suspended,
		CustomCSS:      a.CustomCSS,
		EnableRSS:      *a.EnableRSS,
		Role:           role,
	}

	c.ensureAvatar(accountFrontend)
	c.ensureHeader(accountFrontend)

	return accountFrontend, nil
}

func (c *converter) AccountToAPIAccountBlocked(ctx context.Context, a *gtsmodel.Account) (*apimodel.Account, error) {
	var acct string
	if a.Domain != "" {
		// this is a remote user
		acct = fmt.Sprintf("%s@%s", a.Username, a.Domain)
	} else {
		// this is a local user
		acct = a.Username
	}

	var suspended bool
	if !a.SuspendedAt.IsZero() {
		suspended = true
	}

	return &apimodel.Account{
		ID:          a.ID,
		Username:    a.Username,
		Acct:        acct,
		DisplayName: a.DisplayName,
		Bot:         *a.Bot,
		CreatedAt:   util.FormatISO8601(a.CreatedAt),
		URL:         a.URL,
		Suspended:   suspended,
	}, nil
}

func (c *converter) AccountToAdminAPIAccount(ctx context.Context, a *gtsmodel.Account) (*apimodel.AdminAccountInfo, error) {
	var (
		email                  string
		ip                     *string
		domain                 *string
		locale                 string
		confirmed              bool
		inviteRequest          *string
		approved               bool
		disabled               bool
		silenced               bool
		suspended              bool
		role                   apimodel.AccountRole = apimodel.AccountRole{Name: apimodel.AccountRoleUser} // assume user by default
		createdByApplicationID string
	)

	// take user-level information if possible
	if a.Domain != "" {
		domain = &a.Domain
	} else {
		user, err := c.db.GetUserByAccountID(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("AccountToAdminAPIAccount: error getting user from database for account id %s: %w", a.ID, err)
		}

		if user.Email != "" {
			email = user.Email
		} else {
			email = user.UnconfirmedEmail
		}

		if i := user.CurrentSignInIP.String(); i != "<nil>" {
			ip = &i
		}

		locale = user.Locale
		inviteRequest = &user.Account.Reason
		if *user.Admin {
			role = apimodel.AccountRole{Name: apimodel.AccountRoleAdmin}
		} else if *user.Moderator {
			role = apimodel.AccountRole{Name: apimodel.AccountRoleModerator}
		}
		confirmed = !user.ConfirmedAt.IsZero()
		approved = *user.Approved
		disabled = *user.Disabled
		silenced = !user.Account.SilencedAt.IsZero()
		suspended = !user.Account.SuspendedAt.IsZero()
		createdByApplicationID = user.CreatedByApplicationID
	}

	apiAccount, err := c.AccountToAPIAccountPublic(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("AccountToAdminAPIAccount: error converting account to api account for account id %s: %w", a.ID, err)
	}

	return &apimodel.AdminAccountInfo{
		ID:                     a.ID,
		Username:               a.Username,
		Domain:                 domain,
		CreatedAt:              util.FormatISO8601(a.CreatedAt),
		Email:                  email,
		IP:                     ip,
		IPs:                    []interface{}{}, // not implemented,
		Locale:                 locale,
		InviteRequest:          inviteRequest,
		Role:                   role,
		Confirmed:              confirmed,
		Approved:               approved,
		Disabled:               disabled,
		Silenced:               silenced,
		Suspended:              suspended,
		Account:                apiAccount,
		CreatedByApplicationID: createdByApplicationID,
		InvitedByAccountID:     "", // not implemented (yet)
	}, nil
}

func (c *converter) AppToAPIAppSensitive(ctx context.Context, a *gtsmodel.Application) (*apimodel.Application, error) {
	return &apimodel.Application{
		ID:           a.ID,
		Name:         a.Name,
		Website:      a.Website,
		RedirectURI:  a.RedirectURI,
		ClientID:     a.ClientID,
		ClientSecret: a.ClientSecret,
	}, nil
}

func (c *converter) AppToAPIAppPublic(ctx context.Context, a *gtsmodel.Application) (*apimodel.Application, error) {
	return &apimodel.Application{
		Name:    a.Name,
		Website: a.Website,
	}, nil
}

func (c *converter) AttachmentToAPIAttachment(ctx context.Context, a *gtsmodel.MediaAttachment) (apimodel.Attachment, error) {
	apiAttachment := apimodel.Attachment{
		ID:         a.ID,
		Type:       strings.ToLower(string(a.Type)),
		TextURL:    a.URL,
		PreviewURL: a.Thumbnail.URL,
		Meta: apimodel.MediaMeta{
			Original: apimodel.MediaDimensions{
				Width:  a.FileMeta.Original.Width,
				Height: a.FileMeta.Original.Height,
			},
			Small: apimodel.MediaDimensions{
				Width:  a.FileMeta.Small.Width,
				Height: a.FileMeta.Small.Height,
				Size:   strconv.Itoa(a.FileMeta.Small.Width) + "x" + strconv.Itoa(a.FileMeta.Small.Height),
				Aspect: float32(a.FileMeta.Small.Aspect),
			},
		},
		Blurhash: a.Blurhash,
	}

	// nullable fields
	if i := a.URL; i != "" {
		apiAttachment.URL = &i
	}

	if i := a.RemoteURL; i != "" {
		apiAttachment.RemoteURL = &i
	}

	if i := a.Thumbnail.RemoteURL; i != "" {
		apiAttachment.PreviewRemoteURL = &i
	}

	if i := a.Description; i != "" {
		apiAttachment.Description = &i
	}

	// type specific fields
	switch a.Type {
	case gtsmodel.FileTypeImage:
		apiAttachment.Meta.Original.Size = strconv.Itoa(a.FileMeta.Original.Width) + "x" + strconv.Itoa(a.FileMeta.Original.Height)
		apiAttachment.Meta.Original.Aspect = float32(a.FileMeta.Original.Aspect)
		apiAttachment.Meta.Focus = &apimodel.MediaFocus{
			X: a.FileMeta.Focus.X,
			Y: a.FileMeta.Focus.Y,
		}
	case gtsmodel.FileTypeVideo:
		if i := a.FileMeta.Original.Duration; i != nil {
			apiAttachment.Meta.Original.Duration = *i
		}

		if i := a.FileMeta.Original.Framerate; i != nil {
			// the masto api expects this as a string in
			// the format `integer/1`, so 30fps is `30/1`
			round := math.Round(float64(*i))
			fr := strconv.FormatInt(int64(round), 10)
			apiAttachment.Meta.Original.FrameRate = fr + "/1"
		}

		if i := a.FileMeta.Original.Bitrate; i != nil {
			apiAttachment.Meta.Original.Bitrate = int(*i)
		}
	}

	return apiAttachment, nil
}

func (c *converter) MentionToAPIMention(ctx context.Context, m *gtsmodel.Mention) (apimodel.Mention, error) {
	if m.TargetAccount == nil {
		targetAccount, err := c.db.GetAccountByID(ctx, m.TargetAccountID)
		if err != nil {
			return apimodel.Mention{}, err
		}
		m.TargetAccount = targetAccount
	}

	var local bool
	if m.TargetAccount.Domain == "" {
		local = true
	}

	var acct string
	if local {
		acct = m.TargetAccount.Username
	} else {
		acct = fmt.Sprintf("%s@%s", m.TargetAccount.Username, m.TargetAccount.Domain)
	}

	return apimodel.Mention{
		ID:       m.TargetAccount.ID,
		Username: m.TargetAccount.Username,
		URL:      m.TargetAccount.URL,
		Acct:     acct,
	}, nil
}

func (c *converter) EmojiToAPIEmoji(ctx context.Context, e *gtsmodel.Emoji) (apimodel.Emoji, error) {
	var category string
	if e.CategoryID != "" {
		if e.Category == nil {
			var err error
			e.Category, err = c.db.GetEmojiCategory(ctx, e.CategoryID)
			if err != nil {
				return apimodel.Emoji{}, err
			}
		}
		category = e.Category.Name
	}

	return apimodel.Emoji{
		Shortcode:       e.Shortcode,
		URL:             e.ImageURL,
		StaticURL:       e.ImageStaticURL,
		VisibleInPicker: *e.VisibleInPicker,
		Category:        category,
	}, nil
}

func (c *converter) EmojiToAdminAPIEmoji(ctx context.Context, e *gtsmodel.Emoji) (*apimodel.AdminEmoji, error) {
	emoji, err := c.EmojiToAPIEmoji(ctx, e)
	if err != nil {
		return nil, err
	}

	return &apimodel.AdminEmoji{
		Emoji:         emoji,
		ID:            e.ID,
		Disabled:      *e.Disabled,
		Domain:        e.Domain,
		UpdatedAt:     util.FormatISO8601(e.UpdatedAt),
		TotalFileSize: e.ImageFileSize + e.ImageStaticFileSize,
		ContentType:   e.ImageContentType,
		URI:           e.URI,
	}, nil
}

func (c *converter) EmojiCategoryToAPIEmojiCategory(ctx context.Context, category *gtsmodel.EmojiCategory) (*apimodel.EmojiCategory, error) {
	return &apimodel.EmojiCategory{
		ID:   category.ID,
		Name: category.Name,
	}, nil
}

func (c *converter) TagToAPITag(ctx context.Context, t *gtsmodel.Tag) (apimodel.Tag, error) {
	return apimodel.Tag{
		Name: t.Name,
		URL:  t.URL,
	}, nil
}

func (c *converter) StatusToAPIStatus(ctx context.Context, s *gtsmodel.Status, requestingAccount *gtsmodel.Account) (*apimodel.Status, error) {
	repliesCount, err := c.db.CountStatusReplies(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("error counting replies: %s", err)
	}

	reblogsCount, err := c.db.CountStatusReblogs(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("error counting reblogs: %s", err)
	}

	favesCount, err := c.db.CountStatusFaves(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("error counting faves: %s", err)
	}

	var apiRebloggedStatus *apimodel.Status
	if s.BoostOfID != "" {
		// the boosted status might have been set on this struct already so check first before doing db calls
		if s.BoostOf == nil {
			// it's not set so fetch it from the db
			bs, err := c.db.GetStatusByID(ctx, s.BoostOfID)
			if err != nil {
				return nil, fmt.Errorf("error getting boosted status with id %s: %s", s.BoostOfID, err)
			}
			s.BoostOf = bs
		}

		// the boosted account might have been set on this struct already or passed as a param so check first before doing db calls
		if s.BoostOfAccount == nil {
			// it's not set so fetch it from the db
			ba, err := c.db.GetAccountByID(ctx, s.BoostOf.AccountID)
			if err != nil {
				return nil, fmt.Errorf("error getting boosted account %s from status with id %s: %s", s.BoostOf.AccountID, s.BoostOfID, err)
			}
			s.BoostOfAccount = ba
			s.BoostOf.Account = ba
		}

		apiRebloggedStatus, err = c.StatusToAPIStatus(ctx, s.BoostOf, requestingAccount)
		if err != nil {
			return nil, fmt.Errorf("error converting boosted status to apitype: %s", err)
		}
	}

	var apiApplication *apimodel.Application
	if s.CreatedWithApplicationID != "" {
		gtsApplication := &gtsmodel.Application{}
		if err := c.db.GetByID(ctx, s.CreatedWithApplicationID, gtsApplication); err != nil {
			return nil, fmt.Errorf("error fetching application used to create status: %s", err)
		}
		apiApplication, err = c.AppToAPIAppPublic(ctx, gtsApplication)
		if err != nil {
			return nil, fmt.Errorf("error parsing application used to create status: %s", err)
		}
	}

	if s.Account == nil {
		a, err := c.db.GetAccountByID(ctx, s.AccountID)
		if err != nil {
			return nil, fmt.Errorf("error getting status author: %s", err)
		}
		s.Account = a
	}

	apiAuthorAccount, err := c.AccountToAPIAccountPublic(ctx, s.Account)
	if err != nil {
		return nil, fmt.Errorf("error parsing account of status author: %s", err)
	}

	// convert status gts model attachments to frontend api model attachments
	apiAttachments, err := c.convertAttachmentsToAPIAttachments(ctx, s.Attachments, s.AttachmentIDs)
	if err != nil {
		log.Errorf(ctx, "error converting status attachments: %v", err)
	}

	// convert status gts model mentions to frontend api model mentions
	apiMentions, err := c.convertMentionsToAPIMentions(ctx, s.Mentions, s.MentionIDs)
	if err != nil {
		log.Errorf(ctx, "error converting status mentions: %v", err)
	}

	// convert status gts model tags to frontend api model tags
	apiTags, err := c.convertTagsToAPITags(ctx, s.Tags, s.TagIDs)
	if err != nil {
		log.Errorf(ctx, "error converting status tags: %v", err)
	}

	// convert status gts model emojis to frontend api model emojis
	apiEmojis, err := c.convertEmojisToAPIEmojis(ctx, s.Emojis, s.EmojiIDs)
	if err != nil {
		log.Errorf(ctx, "error converting status emojis: %v", err)
	}

	// Fetch status interaction flags for acccount
	interacts, err := c.interactionsWithStatusForAccount(ctx, s, requestingAccount)
	if err != nil {
		log.Errorf(ctx, "error getting interactions for status %s for account %s: %v", s.ID, requestingAccount.ID, err)

		// Ensure a non nil object
		interacts = &statusInteractions{}
	}

	var language *string
	if s.Language != "" {
		language = &s.Language
	}

	apiStatus := &apimodel.Status{
		ID:                 s.ID,
		CreatedAt:          util.FormatISO8601(s.CreatedAt),
		InReplyToID:        nil,
		InReplyToAccountID: nil,
		Sensitive:          *s.Sensitive,
		SpoilerText:        s.ContentWarning,
		Visibility:         c.VisToAPIVis(ctx, s.Visibility),
		Language:           language,
		URI:                s.URI,
		URL:                s.URL,
		RepliesCount:       repliesCount,
		ReblogsCount:       reblogsCount,
		FavouritesCount:    favesCount,
		Favourited:         interacts.Faved,
		Bookmarked:         interacts.Bookmarked,
		Muted:              interacts.Muted,
		Reblogged:          interacts.Reblogged,
		Pinned:             *s.Pinned,
		Content:            s.Content,
		Reblog:             nil,
		Application:        apiApplication,
		Account:            apiAuthorAccount,
		MediaAttachments:   apiAttachments,
		Mentions:           apiMentions,
		Tags:               apiTags,
		Emojis:             apiEmojis,
		Card:               nil, // TODO: implement cards
		Poll:               nil, // TODO: implement polls
		Text:               s.Text,
	}

	// nullable fields
	if s.InReplyToID != "" {
		i := s.InReplyToID
		apiStatus.InReplyToID = &i
	}

	if s.InReplyToAccountID != "" {
		i := s.InReplyToAccountID
		apiStatus.InReplyToAccountID = &i
	}

	if apiRebloggedStatus != nil {
		apiStatus.Reblog = &apimodel.StatusReblogged{Status: apiRebloggedStatus}
	}

	return apiStatus, nil
}

// VisToapi converts a gts visibility into its api equivalent
func (c *converter) VisToAPIVis(ctx context.Context, m gtsmodel.Visibility) apimodel.Visibility {
	switch m {
	case gtsmodel.VisibilityPublic:
		return apimodel.VisibilityPublic
	case gtsmodel.VisibilityUnlocked:
		return apimodel.VisibilityUnlisted
	case gtsmodel.VisibilityFollowersOnly, gtsmodel.VisibilityMutualsOnly:
		return apimodel.VisibilityPrivate
	case gtsmodel.VisibilityDirect:
		return apimodel.VisibilityDirect
	}
	return ""
}

func (c *converter) InstanceToAPIV1Instance(ctx context.Context, i *gtsmodel.Instance) (*apimodel.InstanceV1, error) {
	instance := &apimodel.InstanceV1{
		URI:              i.URI,
		AccountDomain:    config.GetAccountDomain(),
		Title:            i.Title,
		Description:      i.Description,
		ShortDescription: i.ShortDescription,
		Email:            i.ContactEmail,
		Version:          config.GetSoftwareVersion(),
		Languages:        []string{}, // todo: not supported yet
		Registrations:    config.GetAccountsRegistrationOpen(),
		ApprovalRequired: config.GetAccountsApprovalRequired(),
		InvitesEnabled:   false, // todo: not supported yet
		MaxTootChars:     uint(config.GetStatusesMaxChars()),
	}

	// configuration
	instance.Configuration.Statuses.MaxCharacters = config.GetStatusesMaxChars()
	instance.Configuration.Statuses.MaxMediaAttachments = config.GetStatusesMediaMaxFiles()
	instance.Configuration.Statuses.CharactersReservedPerURL = instanceStatusesCharactersReservedPerURL
	instance.Configuration.MediaAttachments.SupportedMimeTypes = media.SupportedMIMETypes
	instance.Configuration.MediaAttachments.ImageSizeLimit = int(config.GetMediaImageMaxSize())
	instance.Configuration.MediaAttachments.ImageMatrixLimit = instanceMediaAttachmentsImageMatrixLimit
	instance.Configuration.MediaAttachments.VideoSizeLimit = int(config.GetMediaVideoMaxSize())
	instance.Configuration.MediaAttachments.VideoFrameRateLimit = instanceMediaAttachmentsVideoFrameRateLimit
	instance.Configuration.MediaAttachments.VideoMatrixLimit = instanceMediaAttachmentsVideoMatrixLimit
	instance.Configuration.Polls.MaxOptions = config.GetStatusesPollMaxOptions()
	instance.Configuration.Polls.MaxCharactersPerOption = config.GetStatusesPollOptionMaxChars()
	instance.Configuration.Polls.MinExpiration = instancePollsMinExpiration
	instance.Configuration.Polls.MaxExpiration = instancePollsMaxExpiration
	instance.Configuration.Accounts.AllowCustomCSS = config.GetAccountsAllowCustomCSS()
	instance.Configuration.Accounts.MaxFeaturedTags = instanceAccountsMaxFeaturedTags
	instance.Configuration.Emojis.EmojiSizeLimit = int(config.GetMediaEmojiLocalMaxSize())

	// URLs
	instance.URLs.StreamingAPI = "wss://" + i.Domain

	// statistics
	stats := make(map[string]int, 3)
	userCount, err := c.db.CountInstanceUsers(ctx, i.Domain)
	if err != nil {
		return nil, fmt.Errorf("InstanceToAPIV1Instance: db error getting counting instance users: %w", err)
	}
	stats["user_count"] = userCount

	statusCount, err := c.db.CountInstanceStatuses(ctx, i.Domain)
	if err != nil {
		return nil, fmt.Errorf("InstanceToAPIV1Instance: db error getting counting instance statuses: %w", err)
	}
	stats["status_count"] = statusCount

	domainCount, err := c.db.CountInstanceDomains(ctx, i.Domain)
	if err != nil {
		return nil, fmt.Errorf("InstanceToAPIV1Instance: db error getting counting instance domains: %w", err)
	}
	stats["domain_count"] = domainCount
	instance.Stats = stats

	// thumbnail
	iAccount, err := c.db.GetInstanceAccount(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("InstanceToAPIV1Instance: db error getting instance account: %w", err)
	}

	if iAccount.AvatarMediaAttachmentID != "" {
		if iAccount.AvatarMediaAttachment == nil {
			avi, err := c.db.GetAttachmentByID(ctx, iAccount.AvatarMediaAttachmentID)
			if err != nil {
				return nil, fmt.Errorf("InstanceToAPIInstance: error getting instance avatar attachment with id %s: %w", iAccount.AvatarMediaAttachmentID, err)
			}
			iAccount.AvatarMediaAttachment = avi
		}

		instance.Thumbnail = iAccount.AvatarMediaAttachment.URL
		instance.ThumbnailType = iAccount.AvatarMediaAttachment.File.ContentType
		instance.ThumbnailDescription = iAccount.AvatarMediaAttachment.Description
	} else {
		instance.Thumbnail = config.GetProtocol() + "://" + i.Domain + "/assets/logo.png" // default thumb
	}

	// contact account
	if i.ContactAccountID != "" {
		if i.ContactAccount == nil {
			contactAccount, err := c.db.GetAccountByID(ctx, i.ContactAccountID)
			if err != nil {
				return nil, fmt.Errorf("InstanceToAPIV1Instance: db error getting instance contact account %s: %w", i.ContactAccountID, err)
			}
			i.ContactAccount = contactAccount
		}

		account, err := c.AccountToAPIAccountPublic(ctx, i.ContactAccount)
		if err != nil {
			return nil, fmt.Errorf("InstanceToAPIV1Instance: error converting instance contact account %s: %w", i.ContactAccountID, err)
		}
		instance.ContactAccount = account
	}

	return instance, nil
}

func (c *converter) InstanceToAPIV2Instance(ctx context.Context, i *gtsmodel.Instance) (*apimodel.InstanceV2, error) {
	instance := &apimodel.InstanceV2{
		Domain:        i.Domain,
		AccountDomain: config.GetAccountDomain(),
		Title:         i.Title,
		Version:       config.GetSoftwareVersion(),
		SourceURL:     instanceSourceURL,
		Description:   i.Description,
		Usage:         apimodel.InstanceV2Usage{}, // todo: not implemented
		Languages:     []string{},                 // todo: not implemented
		Rules:         []interface{}{},            // todo: not implemented
	}

	// thumbnail
	thumbnail := apimodel.InstanceV2Thumbnail{}

	iAccount, err := c.db.GetInstanceAccount(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("InstanceToAPIV2Instance: db error getting instance account: %w", err)
	}

	if iAccount.AvatarMediaAttachmentID != "" {
		if iAccount.AvatarMediaAttachment == nil {
			avi, err := c.db.GetAttachmentByID(ctx, iAccount.AvatarMediaAttachmentID)
			if err != nil {
				return nil, fmt.Errorf("InstanceToAPIV2Instance: error getting instance avatar attachment with id %s: %w", iAccount.AvatarMediaAttachmentID, err)
			}
			iAccount.AvatarMediaAttachment = avi
		}

		thumbnail.URL = iAccount.AvatarMediaAttachment.URL
		thumbnail.Type = iAccount.AvatarMediaAttachment.File.ContentType
		thumbnail.Description = iAccount.AvatarMediaAttachment.Description
		thumbnail.Blurhash = iAccount.AvatarMediaAttachment.Blurhash
	} else {
		thumbnail.URL = config.GetProtocol() + "://" + i.Domain + "/assets/logo.png" // default thumb
	}

	instance.Thumbnail = thumbnail

	// configuration
	instance.Configuration.URLs.Streaming = "wss://" + i.Domain
	instance.Configuration.Statuses.MaxCharacters = config.GetStatusesMaxChars()
	instance.Configuration.Statuses.MaxMediaAttachments = config.GetStatusesMediaMaxFiles()
	instance.Configuration.Statuses.CharactersReservedPerURL = instanceStatusesCharactersReservedPerURL
	instance.Configuration.MediaAttachments.SupportedMimeTypes = media.SupportedMIMETypes
	instance.Configuration.MediaAttachments.ImageSizeLimit = int(config.GetMediaImageMaxSize())
	instance.Configuration.MediaAttachments.ImageMatrixLimit = instanceMediaAttachmentsImageMatrixLimit
	instance.Configuration.MediaAttachments.VideoSizeLimit = int(config.GetMediaVideoMaxSize())
	instance.Configuration.MediaAttachments.VideoFrameRateLimit = instanceMediaAttachmentsVideoFrameRateLimit
	instance.Configuration.MediaAttachments.VideoMatrixLimit = instanceMediaAttachmentsVideoMatrixLimit
	instance.Configuration.Polls.MaxOptions = config.GetStatusesPollMaxOptions()
	instance.Configuration.Polls.MaxCharactersPerOption = config.GetStatusesPollOptionMaxChars()
	instance.Configuration.Polls.MinExpiration = instancePollsMinExpiration
	instance.Configuration.Polls.MaxExpiration = instancePollsMaxExpiration
	instance.Configuration.Accounts.AllowCustomCSS = config.GetAccountsAllowCustomCSS()
	instance.Configuration.Accounts.MaxFeaturedTags = instanceAccountsMaxFeaturedTags
	instance.Configuration.Emojis.EmojiSizeLimit = int(config.GetMediaEmojiLocalMaxSize())

	// registrations
	instance.Registrations.Enabled = config.GetAccountsRegistrationOpen()
	instance.Registrations.ApprovalRequired = config.GetAccountsApprovalRequired()
	instance.Registrations.Message = nil // todo: not implemented

	// contact
	instance.Contact.Email = i.ContactEmail
	if i.ContactAccountID != "" {
		if i.ContactAccount == nil {
			contactAccount, err := c.db.GetAccountByID(ctx, i.ContactAccountID)
			if err != nil {
				return nil, fmt.Errorf("InstanceToAPIV2Instance: db error getting instance contact account %s: %w", i.ContactAccountID, err)
			}
			i.ContactAccount = contactAccount
		}

		account, err := c.AccountToAPIAccountPublic(ctx, i.ContactAccount)
		if err != nil {
			return nil, fmt.Errorf("InstanceToAPIV2Instance: error converting instance contact account %s: %w", i.ContactAccountID, err)
		}
		instance.Contact.Account = account
	}

	return instance, nil
}

func (c *converter) RelationshipToAPIRelationship(ctx context.Context, r *gtsmodel.Relationship) (*apimodel.Relationship, error) {
	return &apimodel.Relationship{
		ID:                  r.ID,
		Following:           r.Following,
		ShowingReblogs:      r.ShowingReblogs,
		Notifying:           r.Notifying,
		FollowedBy:          r.FollowedBy,
		Blocking:            r.Blocking,
		BlockedBy:           r.BlockedBy,
		Muting:              r.Muting,
		MutingNotifications: r.MutingNotifications,
		Requested:           r.Requested,
		DomainBlocking:      r.DomainBlocking,
		Endorsed:            r.Endorsed,
		Note:                r.Note,
	}, nil
}

func (c *converter) NotificationToAPINotification(ctx context.Context, n *gtsmodel.Notification) (*apimodel.Notification, error) {
	if n.TargetAccount == nil {
		tAccount, err := c.db.GetAccountByID(ctx, n.TargetAccountID)
		if err != nil {
			return nil, fmt.Errorf("NotificationToapi: error getting target account with id %s from the db: %s", n.TargetAccountID, err)
		}
		n.TargetAccount = tAccount
	}

	if n.OriginAccount == nil {
		ogAccount, err := c.db.GetAccountByID(ctx, n.OriginAccountID)
		if err != nil {
			return nil, fmt.Errorf("NotificationToapi: error getting origin account with id %s from the db: %s", n.OriginAccountID, err)
		}
		n.OriginAccount = ogAccount
	}

	apiAccount, err := c.AccountToAPIAccountPublic(ctx, n.OriginAccount)
	if err != nil {
		return nil, fmt.Errorf("NotificationToapi: error converting account to api: %s", err)
	}

	var apiStatus *apimodel.Status
	if n.StatusID != "" {
		if n.Status == nil {
			status, err := c.db.GetStatusByID(ctx, n.StatusID)
			if err != nil {
				return nil, fmt.Errorf("NotificationToapi: error getting status with id %s from the db: %s", n.StatusID, err)
			}
			n.Status = status
		}

		if n.Status.Account == nil {
			if n.Status.AccountID == n.TargetAccount.ID {
				n.Status.Account = n.TargetAccount
			} else if n.Status.AccountID == n.OriginAccount.ID {
				n.Status.Account = n.OriginAccount
			}
		}

		var err error
		apiStatus, err = c.StatusToAPIStatus(ctx, n.Status, n.TargetAccount)
		if err != nil {
			return nil, fmt.Errorf("NotificationToapi: error converting status to api: %s", err)
		}
	}

	if apiStatus != nil && apiStatus.Reblog != nil {
		// use the actual reblog status for the notifications endpoint
		apiStatus = apiStatus.Reblog.Status
	}

	return &apimodel.Notification{
		ID:        n.ID,
		Type:      string(n.NotificationType),
		CreatedAt: util.FormatISO8601(n.CreatedAt),
		Account:   apiAccount,
		Status:    apiStatus,
	}, nil
}

func (c *converter) DomainBlockToAPIDomainBlock(ctx context.Context, b *gtsmodel.DomainBlock, export bool) (*apimodel.DomainBlock, error) {
	domainBlock := &apimodel.DomainBlock{
		Domain: apimodel.Domain{
			Domain:        b.Domain,
			PublicComment: b.PublicComment,
		},
	}

	// if we're exporting a domain block, return it with minimal information attached
	if !export {
		domainBlock.ID = b.ID
		domainBlock.Obfuscate = *b.Obfuscate
		domainBlock.PrivateComment = b.PrivateComment
		domainBlock.SubscriptionID = b.SubscriptionID
		domainBlock.CreatedBy = b.CreatedByAccountID
		domainBlock.CreatedAt = util.FormatISO8601(b.CreatedAt)
	}

	return domainBlock, nil
}

func (c *converter) ReportToAPIReport(ctx context.Context, r *gtsmodel.Report) (*apimodel.Report, error) {
	report := &apimodel.Report{
		ID:          r.ID,
		CreatedAt:   util.FormatISO8601(r.CreatedAt),
		ActionTaken: !r.ActionTakenAt.IsZero(),
		Category:    "other", // todo: only support default 'other' category right now
		Comment:     r.Comment,
		Forwarded:   *r.Forwarded,
		StatusIDs:   r.StatusIDs,
		RuleIDs:     []int{}, // todo: not supported yet
	}

	if !r.ActionTakenAt.IsZero() {
		actionTakenAt := util.FormatISO8601(r.ActionTakenAt)
		report.ActionTakenAt = &actionTakenAt
	}

	if actionComment := r.ActionTaken; actionComment != "" {
		report.ActionTakenComment = &actionComment
	}

	if r.TargetAccount == nil {
		tAccount, err := c.db.GetAccountByID(ctx, r.TargetAccountID)
		if err != nil {
			return nil, fmt.Errorf("ReportToAPIReport: error getting target account with id %s from the db: %s", r.TargetAccountID, err)
		}
		r.TargetAccount = tAccount
	}

	apiAccount, err := c.AccountToAPIAccountPublic(ctx, r.TargetAccount)
	if err != nil {
		return nil, fmt.Errorf("ReportToAPIReport: error converting target account to api: %s", err)
	}
	report.TargetAccount = apiAccount

	return report, nil
}

func (c *converter) ReportToAdminAPIReport(ctx context.Context, r *gtsmodel.Report, requestingAccount *gtsmodel.Account) (*apimodel.AdminReport, error) {
	var (
		err                  error
		actionTakenAt        *string
		actionTakenComment   *string
		actionTakenByAccount *apimodel.AdminAccountInfo
	)

	if !r.ActionTakenAt.IsZero() {
		ata := util.FormatISO8601(r.ActionTakenAt)
		actionTakenAt = &ata
	}

	if r.Account == nil {
		r.Account, err = c.db.GetAccountByID(ctx, r.AccountID)
		if err != nil {
			return nil, fmt.Errorf("ReportToAdminAPIReport: error getting account with id %s from the db: %w", r.AccountID, err)
		}
	}
	account, err := c.AccountToAdminAPIAccount(ctx, r.Account)
	if err != nil {
		return nil, fmt.Errorf("ReportToAdminAPIReport: error converting account with id %s to adminAPIAccount: %w", r.AccountID, err)
	}

	if r.TargetAccount == nil {
		r.TargetAccount, err = c.db.GetAccountByID(ctx, r.TargetAccountID)
		if err != nil {
			return nil, fmt.Errorf("ReportToAdminAPIReport: error getting target account with id %s from the db: %w", r.TargetAccountID, err)
		}
	}
	targetAccount, err := c.AccountToAdminAPIAccount(ctx, r.TargetAccount)
	if err != nil {
		return nil, fmt.Errorf("ReportToAdminAPIReport: error converting target account with id %s to adminAPIAccount: %w", r.TargetAccountID, err)
	}

	if r.ActionTakenByAccountID != "" {
		if r.ActionTakenByAccount == nil {
			r.ActionTakenByAccount, err = c.db.GetAccountByID(ctx, r.ActionTakenByAccountID)
			if err != nil {
				return nil, fmt.Errorf("ReportToAdminAPIReport: error getting action taken by account with id %s from the db: %w", r.ActionTakenByAccountID, err)
			}
		}

		actionTakenByAccount, err = c.AccountToAdminAPIAccount(ctx, r.ActionTakenByAccount)
		if err != nil {
			return nil, fmt.Errorf("ReportToAdminAPIReport: error converting action taken by account with id %s to adminAPIAccount: %w", r.ActionTakenByAccountID, err)
		}
	}

	statuses := make([]*apimodel.Status, 0, len(r.StatusIDs))
	if len(r.StatusIDs) != 0 && len(r.Statuses) == 0 {
		r.Statuses, err = c.db.GetStatuses(ctx, r.StatusIDs)
		if err != nil {
			return nil, fmt.Errorf("ReportToAdminAPIReport: error getting statuses from the db: %w", err)
		}
	}
	for _, s := range r.Statuses {
		status, err := c.StatusToAPIStatus(ctx, s, requestingAccount)
		if err != nil {
			return nil, fmt.Errorf("ReportToAdminAPIReport: error converting status with id %s to api status: %w", s.ID, err)
		}
		statuses = append(statuses, status)
	}

	if ac := r.ActionTaken; ac != "" {
		actionTakenComment = &ac
	}

	return &apimodel.AdminReport{
		ID:                   r.ID,
		ActionTaken:          !r.ActionTakenAt.IsZero(),
		ActionTakenAt:        actionTakenAt,
		Category:             "other", // todo: only support default 'other' category right now
		Comment:              r.Comment,
		Forwarded:            *r.Forwarded,
		CreatedAt:            util.FormatISO8601(r.CreatedAt),
		UpdatedAt:            util.FormatISO8601(r.UpdatedAt),
		Account:              account,
		TargetAccount:        targetAccount,
		AssignedAccount:      actionTakenByAccount,
		ActionTakenByAccount: actionTakenByAccount,
		ActionTakenComment:   actionTakenComment,
		Statuses:             statuses,
		Rules:                []interface{}{}, // not implemented
	}, nil
}

// convertAttachmentsToAPIAttachments will convert a slice of GTS model attachments to frontend API model attachments, falling back to IDs if no GTS models supplied.
func (c *converter) convertAttachmentsToAPIAttachments(ctx context.Context, attachments []*gtsmodel.MediaAttachment, attachmentIDs []string) ([]apimodel.Attachment, error) {
	var errs gtserror.MultiError

	if len(attachments) == 0 {
		// GTS model attachments were not populated

		// Preallocate expected GTS slice
		attachments = make([]*gtsmodel.MediaAttachment, 0, len(attachmentIDs))

		// Fetch GTS models for attachment IDs
		for _, id := range attachmentIDs {
			attachment, err := c.db.GetAttachmentByID(ctx, id)
			if err != nil {
				errs.Appendf("error fetching attachment %s from database: %v", id, err)
				continue
			}
			attachments = append(attachments, attachment)
		}
	}

	// Preallocate expected frontend slice
	apiAttachments := make([]apimodel.Attachment, 0, len(attachments))

	// Convert GTS models to frontend models
	for _, attachment := range attachments {
		apiAttachment, err := c.AttachmentToAPIAttachment(ctx, attachment)
		if err != nil {
			errs.Appendf("error converting attchment %s to api attachment: %v", attachment.ID, err)
			continue
		}
		apiAttachments = append(apiAttachments, apiAttachment)
	}

	return apiAttachments, errs.Combine()
}

// convertEmojisToAPIEmojis will convert a slice of GTS model emojis to frontend API model emojis, falling back to IDs if no GTS models supplied.
func (c *converter) convertEmojisToAPIEmojis(ctx context.Context, emojis []*gtsmodel.Emoji, emojiIDs []string) ([]apimodel.Emoji, error) {
	var errs gtserror.MultiError

	if len(emojis) == 0 {
		// GTS model attachments were not populated

		// Preallocate expected GTS slice
		emojis = make([]*gtsmodel.Emoji, 0, len(emojiIDs))

		// Fetch GTS models for emoji IDs
		for _, id := range emojiIDs {
			emoji, err := c.db.GetEmojiByID(ctx, id)
			if err != nil {
				errs.Appendf("error fetching emoji %s from database: %v", id, err)
				continue
			}
			emojis = append(emojis, emoji)
		}
	}

	// Preallocate expected frontend slice
	apiEmojis := make([]apimodel.Emoji, 0, len(emojis))

	// Convert GTS models to frontend models
	for _, emoji := range emojis {
		apiEmoji, err := c.EmojiToAPIEmoji(ctx, emoji)
		if err != nil {
			errs.Appendf("error converting emoji %s to api emoji: %v", emoji.ID, err)
			continue
		}
		apiEmojis = append(apiEmojis, apiEmoji)
	}

	return apiEmojis, errs.Combine()
}

// convertMentionsToAPIMentions will convert a slice of GTS model mentions to frontend API model mentions, falling back to IDs if no GTS models supplied.
func (c *converter) convertMentionsToAPIMentions(ctx context.Context, mentions []*gtsmodel.Mention, mentionIDs []string) ([]apimodel.Mention, error) {
	var errs gtserror.MultiError

	if len(mentions) == 0 {
		var err error

		// GTS model mentions were not populated
		//
		// Fetch GTS models for mention IDs
		mentions, err = c.db.GetMentions(ctx, mentionIDs)
		if err != nil {
			errs.Appendf("error fetching mentions from database: %v", err)
		}
	}

	// Preallocate expected frontend slice
	apiMentions := make([]apimodel.Mention, 0, len(mentions))

	// Convert GTS models to frontend models
	for _, mention := range mentions {
		apiMention, err := c.MentionToAPIMention(ctx, mention)
		if err != nil {
			errs.Appendf("error converting mention %s to api mention: %v", mention.ID, err)
			continue
		}
		apiMentions = append(apiMentions, apiMention)
	}

	return apiMentions, errs.Combine()
}

// convertTagsToAPITags will convert a slice of GTS model tags to frontend API model tags, falling back to IDs if no GTS models supplied.
func (c *converter) convertTagsToAPITags(ctx context.Context, tags []*gtsmodel.Tag, tagIDs []string) ([]apimodel.Tag, error) {
	var errs gtserror.MultiError

	if len(tags) == 0 {
		// GTS model tags were not populated

		// Preallocate expected GTS slice
		tags = make([]*gtsmodel.Tag, 0, len(tagIDs))

		// Fetch GTS models for tag IDs
		for _, id := range tagIDs {
			tag := new(gtsmodel.Tag)
			if err := c.db.GetByID(ctx, id, tag); err != nil {
				errs.Appendf("error fetching tag %s from database: %v", id, err)
				continue
			}
			tags = append(tags, tag)
		}
	}

	// Preallocate expected frontend slice
	apiTags := make([]apimodel.Tag, 0, len(tags))

	// Convert GTS models to frontend models
	for _, tag := range tags {
		apiTag, err := c.TagToAPITag(ctx, tag)
		if err != nil {
			errs.Appendf("error converting tag %s to api tag: %v", tag.ID, err)
			continue
		}
		apiTags = append(apiTags, apiTag)
	}

	return apiTags, errs.Combine()
}
