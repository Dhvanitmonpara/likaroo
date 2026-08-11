package controllers

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"linkaroo-app/server/db"
	"linkaroo-app/server/models"
	"linkaroo-app/server/pkg/pipeline"
	pipelineModels "linkaroo-app/server/pkg/pipeline/models"
	"linkaroo-app/server/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func attachTagsToUserLink(userLink *models.UserLink, userID uuid.UUID, tagNames []string) {
	if len(tagNames) == 0 || userLink == nil {
		return
	}

	log.Printf("[Agent Pipeline] Generated tags for link %s (User: %s): %v", userLink.ID, userID, tagNames)

	var tagsToAttach []models.Tag
	for _, name := range tagNames {
		cleanName := strings.TrimSpace(strings.ToLower(name))
		if cleanName == "" {
			continue
		}

		var tag models.Tag
		err := db.DB.Where("tagname = ?", cleanName).First(&tag).Error
		if err != nil {
			// Create tag if not exists
			tag = models.Tag{
				Tagname: cleanName,
				OwnerID: &userID,
			}
			if createErr := db.DB.Create(&tag).Error; createErr != nil {
				log.Printf("[Agent Pipeline] Failed to create tag %q: %v", cleanName, createErr)
				continue
			}
		}
		tagsToAttach = append(tagsToAttach, tag)
	}

	if len(tagsToAttach) > 0 {
		if err := db.DB.Model(userLink).Association("Tags").Append(tagsToAttach); err != nil {
			log.Printf("[Agent Pipeline] Failed to attach tags to UserLink %s: %v", userLink.ID, err)
		} else {
			log.Printf("[Agent Pipeline] Successfully attached %d tag(s) to UserLink %s", len(tagsToAttach), userLink.ID)
		}
	}
}

func getOrCreateLink(targetURL, title, description, image, contentType string) (*models.Link, error) {
	var link models.Link
	err := db.DB.Unscoped().Where("link = ?", targetURL).First(&link).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			link = models.Link{
				LinkURL:     targetURL,
				Title:       title,
				Description: description,
				Image:       image,
				ContentType: contentType,
			}
			if err := db.DB.Create(&link).Error; err != nil {
				return nil, err
			}
			return &link, nil
		}
		return nil, err
	}

	// Update existing record if it had a generic/placeholder title or was previously saved
	updates := map[string]interface{}{}
	if link.Title == "" || link.Title == "Checkout this chat" || link.Title == "ChatGPT" || link.Title == "ChatGPT - Checkout this chat" || (title != "" && title != "Checkout this chat" && title != "ChatGPT") {
		updates["title"] = title
		link.Title = title
	}
	if description != "" {
		updates["description"] = description
		link.Description = description
	}
	if image != "" {
		updates["image"] = image
		link.Image = image
	}
	if contentType != "" {
		updates["content_type"] = contentType
		link.ContentType = contentType
	}

	if link.DeletedAt.Valid {
		updates["deleted_at"] = nil
		link.DeletedAt = gorm.DeletedAt{}
	}

	if len(updates) > 0 {
		db.DB.Unscoped().Model(&link).Updates(updates)
	}

	return &link, nil
}

func getOrCreateUserLink(userID, collectionID, linkID uuid.UUID, customTitle, customDescription *string) (*models.UserLink, error) {
	var userLink models.UserLink
	err := db.DB.Unscoped().Where("user_id = ? AND collection_id = ? AND link_id = ?", userID, collectionID, linkID).First(&userLink).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			userLink = models.UserLink{
				UserID:            userID,
				CollectionID:      collectionID,
				LinkID:            linkID,
				CustomTitle:       customTitle,
				CustomDescription: customDescription,
			}
			if err := db.DB.Create(&userLink).Error; err != nil {
				return nil, err
			}
			return &userLink, nil
		}
		return nil, err
	}

	updates := map[string]interface{}{}
	if userLink.CustomTitle == nil || *userLink.CustomTitle == "" || *userLink.CustomTitle == "Checkout this chat" || *userLink.CustomTitle == "ChatGPT" || *userLink.CustomTitle == "ChatGPT - Checkout this chat" {
		if customTitle != nil && *customTitle != "" && *customTitle != "Checkout this chat" {
			updates["custom_title"] = customTitle
			userLink.CustomTitle = customTitle
		}
	}

	if userLink.DeletedAt.Valid {
		updates["deleted_at"] = nil
		updates["custom_description"] = customDescription
		userLink.DeletedAt = gorm.DeletedAt{}
	}

	if len(updates) > 0 {
		db.DB.Unscoped().Model(&userLink).Updates(updates)
	}

	return &userLink, nil
}

func CreateLink(c *gin.Context) {
	collectionIDStr := c.Param("collectionId")
	collectionID, err := uuid.Parse(collectionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid collection ID"})
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Link        string `json:"link"`
		UserId      string `json:"userId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	ghToken := GetActiveGitHubTokenForUser(req.UserId)
	gptToken := GetActiveChatGPTTokenForUser(req.UserId)

	// Execute processing pipeline (Metadata Scraping -> Canonical Mapping -> LangChain Agent Workflow)
	pipe := pipeline.NewPipeline(nil)
	pipeResult, _ := pipe.Process(c.Request.Context(), pipelineModels.RawItem{
		URL: req.Link,
		Headers: map[string]string{
			"GitHub-Token":  ghToken,
			"ChatGPT-Token": gptToken,
		},
	})

	meta := utils.FetchMetadataWithToken(req.Link, ghToken, gptToken)
	isReachable := meta.Title != "" || meta.Description != ""

	title := strings.TrimSpace(req.Title)
	if title == "" || title == req.Link || strings.HasPrefix(title, "http://") || strings.HasPrefix(title, "https://") {
		if pipeResult != nil && pipeResult.Title != "" {
			title = pipeResult.Title
		} else if meta.Title != "" {
			title = meta.Title
		} else {
			title = utils.GenerateTitleFromURL(req.Link)
		}
	}

	description := req.Description
	if description == "" {
		if pipeResult != nil && pipeResult.Notes != "" {
			description = pipeResult.Notes
		} else {
			description = meta.Description
		}
	}

	linkObj, err := getOrCreateLink(req.Link, title, description, meta.Image, utils.DetectContentType(req.Link, meta.Type))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create link"})
		return
	}

	userLinkObj, err := getOrCreateUserLink(userID, collectionID, linkObj.ID, &title, &description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map link to user"})
		return
	}

	// Attach generated tags from pipeline agent workflow to user link
	if pipeResult != nil && len(pipeResult.Tags) > 0 {
		attachTagsToUserLink(userLinkObj, userID, pipeResult.Tags)
	}

	link := *linkObj
	userLink := *userLinkObj

	db.DB.Preload("Link").Preload("Tags").Preload("Tasks").First(&userLink, userLink.ID)

	customLink := gin.H{
		"title":        title,
		"description":  description,
		"collectionId": collectionID,
		"link":         link.LinkURL,
		"image":        meta.Image,
		"isChecked":    false,
		"contentType":  link.ContentType,
		"tags":         userLink.Tags,
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": gin.H{
			"data":            customLink,
			"isLinkReachable": isReachable,
		},
	})
}

func GetAllLinks(c *gin.Context) {
	var userLinks []models.UserLink
	// Need to preload Link since formatLinks expects linkId object
	if err := db.DB.Preload("Link").Preload("Tags").Preload("Tasks").Find(&userLinks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch links"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": userLinks,
	})
}

func GetLinksByCollection(c *gin.Context) {
	collectionIDStr := c.Param("collectionId")
	collectionID, err := uuid.Parse(collectionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid collection ID"})
		return
	}

	var userLinks []models.UserLink
	if err := db.DB.Preload("Link").Preload("Tags").Preload("Tasks").Where("collection_id = ?", collectionID).Find(&userLinks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch collection links"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": userLinks,
	})
}

func QuickAddLink(c *gin.Context) {
	collectionIDStr := c.Param("collectionId")
	collectionID, err := uuid.Parse(collectionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid collection ID"})
		return
	}

	var req struct {
		Link   string `json:"link"`
		UserId string `json:"userId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	rawInput := strings.TrimSpace(req.Link)
	targetURL := rawInput
	isLink := false

	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		if strings.HasPrefix(targetURL, "www.") || regexp.MustCompile(`^(?i)[a-z0-9-]+(\.[a-z0-9-]+)+(/.*)?$`).MatchString(targetURL) {
			targetURL = "https://" + targetURL
		}
	}

	u, err := url.ParseRequestURI(targetURL)
	if err == nil && u.Scheme != "" && u.Host != "" && strings.Contains(u.Host, ".") {
		isLink = true
	}

	var link models.Link
	var customTitle *string
	var customDescription *string

	pipe := pipeline.NewPipeline(nil)
	var rawItem pipelineModels.RawItem
	if isLink {
		rawItem = pipelineModels.RawItem{URL: targetURL}
	} else {
		rawItem = pipelineModels.RawItem{Text: rawInput}
	}
	pipeResult, _ := pipe.Process(c.Request.Context(), rawItem)

	if isLink {
		ghToken := GetActiveGitHubTokenForUser(req.UserId)
		gptToken := GetActiveChatGPTTokenForUser(req.UserId)
		meta := utils.FetchMetadataWithToken(targetURL, ghToken, gptToken)
		title := meta.Title
		if title == "" && pipeResult != nil && pipeResult.Title != "" {
			title = pipeResult.Title
		}
		if title == "" {
			title = utils.GenerateTitleFromURL(targetURL)
		}
		description := meta.Description
		if description == "" && pipeResult != nil && pipeResult.Notes != "" {
			description = pipeResult.Notes
		}
		contentType := utils.DetectContentType(targetURL, meta.Type)

		linkObj, err := getOrCreateLink(targetURL, title, description, meta.Image, contentType)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create link"})
			return
		}
		link = *linkObj

		customTitle = &title
		customDescription = &description
	} else {
		defaultTitle := ""
		noteDesc := rawInput
		if pipeResult != nil && pipeResult.Title != "" {
			defaultTitle = pipeResult.Title
		}
		if pipeResult != nil && pipeResult.Notes != "" {
			noteDesc = pipeResult.Notes
		}
		customTitle = &defaultTitle
		customDescription = &noteDesc

		link = models.Link{
			Title:       defaultTitle,
			Description: noteDesc,
			LinkURL:     uuid.New().String(),
			ContentType: "note",
		}
		if err := db.DB.Create(&link).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create note"})
			return
		}
	}

	userLinkObj, err := getOrCreateUserLink(userID, collectionID, link.ID, customTitle, customDescription)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map link to user"})
		return
	}

	if pipeResult != nil && len(pipeResult.Tags) > 0 {
		attachTagsToUserLink(userLinkObj, userID, pipeResult.Tags)
	}

	userLink := *userLinkObj
	db.DB.Preload("Link").Preload("Tags").Preload("Tasks").First(&userLink, userLink.ID)

	c.JSON(http.StatusCreated, gin.H{
		"data": gin.H{
			"link":     link,
			"userLink": userLink,
		},
	})
}

func CreateCard(c *gin.Context) {
	collectionIDStr := c.Param("collectionId")
	collectionID, err := uuid.Parse(collectionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid collection ID"})
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Link        string `json:"link"`
		UserId      string `json:"userId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Determine if it's a valid URL
	isLink := false
	u, err := url.ParseRequestURI(req.Link)
	if err == nil && u.Scheme != "" && u.Host != "" {
		isLink = true
	}

	pipe := pipeline.NewPipeline(nil)
	var cardRawItem pipelineModels.RawItem
	if isLink {
		cardRawItem = pipelineModels.RawItem{URL: req.Link}
	} else {
		cardRawItem = pipelineModels.RawItem{Text: req.Title + " " + req.Description}
	}
	pipeResult, _ := pipe.Process(c.Request.Context(), cardRawItem)

	var link models.Link
	if isLink {
		ghToken := GetActiveGitHubTokenForUser(req.UserId)
		meta := utils.FetchMetadataWithToken(req.Link, ghToken)
		title := req.Title
		if title == "" && pipeResult != nil && pipeResult.Title != "" {
			title = pipeResult.Title
		}
		if title == "" {
			title = meta.Title
		}
		if title == "" {
			title = utils.GenerateTitleFromURL(req.Link)
		}

		description := req.Description
		if description == "" && pipeResult != nil && pipeResult.Notes != "" {
			description = pipeResult.Notes
		}
		if description == "" {
			description = meta.Description
		}

		linkObj, err := getOrCreateLink(req.Link, title, description, meta.Image, utils.DetectContentType(req.Link, meta.Type))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create link"})
			return
		}
		link = *linkObj
	} else {
		// Content based card (note)
		cardDesc := req.Description
		if cardDesc == "" && pipeResult != nil && pipeResult.Notes != "" {
			cardDesc = pipeResult.Notes
		}

		link = models.Link{
			Title:       req.Title,
			Description: cardDesc,
			LinkURL:     uuid.New().String(), // Unique string to satisfy DB constraints
			ContentType: "note",
		}
		if err := db.DB.Create(&link).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create link"})
			return
		}
	}

	userLinkObj, err := getOrCreateUserLink(userID, collectionID, link.ID, &link.Title, &link.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to map link to user"})
		return
	}

	if pipeResult != nil && len(pipeResult.Tags) > 0 {
		attachTagsToUserLink(userLinkObj, userID, pipeResult.Tags)
	}

	userLink := *userLinkObj
	db.DB.Preload("Link").Preload("Tags").Preload("Tasks").First(&userLink, userLink.ID)

	customLink := gin.H{
		"title":        link.Title,
		"description":  link.Description,
		"collectionId": collectionID,
		"link":         link.LinkURL,
		"image":        link.Image,
		"isChecked":    false,
		"contentType":  link.ContentType,
		"tags":         userLink.Tags,
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": customLink,
	})
}

func DeleteLink(c *gin.Context) {
	linkIDStr := c.Param("linkId")
	linkID, err := uuid.Parse(linkIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid link ID"})
		return
	}

	var userLink models.UserLink
	if err := db.DB.First(&userLink, linkID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Link not found"})
		return
	}

	// Delete the UserLink
	if err := db.DB.Delete(&userLink).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete link"})
		return
	}

	// Check if any other users still reference this link
	var otherReferences int64
	db.DB.Model(&models.UserLink{}).Where("link_id = ?", userLink.LinkID).Count(&otherReferences)

	// If no other users have this link, delete the actual Link document
	if otherReferences == 0 {
		db.DB.Delete(&models.Link{}, "id = ?", userLink.LinkID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Link deleted successfully"})
}

func UpdateLink(c *gin.Context) {
	linkIDStr := c.Param("linkId")
	linkID, err := uuid.Parse(linkIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid link ID"})
		return
	}

	var req struct {
		Title        *string                `json:"title"`
		Description  *string                `json:"description"`
		CollectionId *string                `json:"collectionId"`
		Tags         *[]models.Tag          `json:"tags"`
		Tasks        *[]models.UserLinkTask `json:"tasks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var userLink models.UserLink
	if err := db.DB.Preload("Tags").Preload("Tasks").First(&userLink, linkID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Link not found"})
		return
	}

	updates := map[string]interface{}{}
	if req.Title != nil {
		updates["custom_title"] = req.Title
	}
	if req.Description != nil {
		updates["custom_description"] = req.Description
	}
	if req.CollectionId != nil {
		collectionID, err := uuid.Parse(*req.CollectionId)
		if err == nil {
			updates["collection_id"] = collectionID
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid collection ID"})
			return
		}
	}

	if err := db.DB.Model(&userLink).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update link"})
		return
	}

	if req.Tags != nil {
		db.DB.Model(&userLink).Association("Tags").Replace(*req.Tags)
	}

	if req.Tasks != nil {
		// Delete all existing tasks for this link, then recreate from the request
		db.DB.Where("user_link_id = ?", userLink.ID).Delete(&models.UserLinkTask{})
		for _, task := range *req.Tasks {
			newTask := models.UserLinkTask{
				UserLinkID: userLink.ID,
				Title:      task.Title,
				Date:       task.Date,
				Completed:  task.Completed,
			}
			db.DB.Create(&newTask)
		}
	}

	db.DB.Preload("Link").Preload("Tags").Preload("Tasks").First(&userLink, userLink.ID)

	c.JSON(http.StatusOK, gin.H{
		"data": userLink,
	})
}
