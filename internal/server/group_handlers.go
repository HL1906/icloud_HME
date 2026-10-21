package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/aliasmeta"
)

// 分组与别名元数据接口。
//
// 这一组数据完全属于本系统:分组和备注不会写到 iCloud,
// 删除账号或别名时也不需要回头去改 Apple 那边的东西。
// iCloud 只提供"邮箱地址 + 标签 + 启用状态 + 创建时间"这四个字段,
// 其中标签创建后不可修改,因此"编辑"在本系统里编辑的是分组和备注。

// groupView 在分组上附带本系统登记的别名数量。
type groupView struct {
	aliasmeta.Group
	AliasCount int `json:"alias_count"`
}

// mapMetaErr 把存储层错误映射成 HTTP 状态码。
func mapMetaErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aliasmeta.ErrGroupNotFound):
		failCode(c, http.StatusNotFound, "GROUP_NOT_FOUND", "分组不存在")
	case errors.Is(err, aliasmeta.ErrGroupNameTaken):
		failCode(c, http.StatusBadRequest, "GROUP_NAME_TAKEN", "分组名已存在")
	case errors.Is(err, aliasmeta.ErrGroupNameEmpty),
		errors.Is(err, aliasmeta.ErrGroupNameTooLong),
		errors.Is(err, aliasmeta.ErrGroupLimit),
		errors.Is(err, aliasmeta.ErrNoteTooLong),
		errors.Is(err, aliasmeta.ErrInvalidEmail):
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	default:
		failCode(c, http.StatusInternalServerError, "STORAGE_FAILURE", "保存失败,请稍后重试")
	}
}

// listGroupsHandler 处理 GET /api/groups。
func (s *Server) listGroupsHandler(c *gin.Context) {
	counts := make(map[string]int)
	for _, entry := range s.meta.ListEntries() {
		if entry.GroupID != "" {
			counts[entry.GroupID]++
		}
	}
	groups := s.meta.ListGroups()
	views := make([]groupView, 0, len(groups))
	for _, g := range groups {
		views = append(views, groupView{Group: g, AliasCount: counts[g.ID]})
	}
	ok(c, gin.H{"groups": views, "count": len(views)})
}

type groupReq struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Note  string `json:"note"`
}

// createGroupHandler 处理 POST /api/groups。
func (s *Server) createGroupHandler(c *gin.Context) {
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	group, err := s.meta.CreateGroup(req.Name, req.Color, req.Note)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, groupView{Group: group})
}

// updateGroupHandler 处理 PATCH /api/groups/:id(局部更新)。
func (s *Server) updateGroupHandler(c *gin.Context) {
	var patch aliasmeta.GroupPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	group, err := s.meta.UpdateGroup(c.Param("id"), patch)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, groupView{Group: group})
}

// deleteGroupHandler 处理 DELETE /api/groups/:id。
//
// 删除分组不会动 iCloud 上的任何邮箱,只把组内别名改回"未分组"。
func (s *Server) deleteGroupHandler(c *gin.Context) {
	unassigned, err := s.meta.DeleteGroup(c.Param("id"))
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, gin.H{"deleted": true, "unassigned": unassigned})
}

// listAliasMetaHandler 处理 GET /api/alias-meta[?account_id=]。
func (s *Server) listAliasMetaHandler(c *gin.Context) {
	accountID := strings.TrimSpace(c.Query("account_id"))
	entries := s.meta.ListEntries()
	if accountID != "" {
		filtered := make([]aliasmeta.Entry, 0, len(entries))
		for _, entry := range entries {
			if entry.AccountID == accountID {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	ok(c, gin.H{"entries": entries, "count": len(entries)})
}

type aliasMetaReq struct {
	Email     string `json:"email"`
	AccountID string `json:"account_id"`
	GroupID   string `json:"group_id"`
	Note      string `json:"note"`
}

// putAliasMetaHandler 处理 PUT /api/alias-meta。
//
// 语义是整体覆盖:group_id 与 note 都传空表示清除该别名的本地元数据。
func (s *Server) putAliasMetaHandler(c *gin.Context) {
	var req aliasMetaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: email 必填")
		return
	}
	entry, err := s.meta.Upsert(req.Email, req.AccountID, req.GroupID, req.Note)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, entry)
}

// deleteAliasMetaHandler 处理 DELETE /api/alias-meta?email=。
func (s *Server) deleteAliasMetaHandler(c *gin.Context) {
	email := strings.TrimSpace(c.Query("email"))
	if email == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数缺失: email")
		return
	}
	deleted, err := s.meta.DeleteEntry(email)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, gin.H{"email": email, "deleted": deleted})
}
