package server

// 标注(tag)接口。
//
// 标注是"第二套标签":iCloud 的 label 建号后不可修改,而"这个号干什么用、
// 谁在用"这类信息会变,所以本系统自己维护一套可编辑、可复用的标注目录。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"icloud-hme/internal/aliasmeta"
)

// tagView 在标注上附带被多少个别名使用。
type tagView struct {
	aliasmeta.Tag
	AliasCount int `json:"alias_count"`
}

// listTagsHandler 处理 GET /api/tags。
func (s *Server) listTagsHandler(c *gin.Context) {
	usage := s.meta.TagUsage()
	tags := s.meta.ListTags()
	views := make([]tagView, 0, len(tags))
	for _, tag := range tags {
		views = append(views, tagView{Tag: tag, AliasCount: usage[tag.ID]})
	}
	ok(c, gin.H{"tags": views, "count": len(views)})
}

type tagReq struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Color string `json:"color"`
	Note  string `json:"note"`
}

// createTagHandler 处理 POST /api/tags。
func (s *Server) createTagHandler(c *gin.Context) {
	var req tagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	tag, err := s.meta.CreateTag(req.Name, req.Kind, req.Color, req.Note)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, tagView{Tag: tag})
}

// updateTagHandler 处理 PATCH /api/tags/:id(局部更新)。
func (s *Server) updateTagHandler(c *gin.Context) {
	var patch aliasmeta.TagPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	tag, err := s.meta.UpdateTag(c.Param("id"), patch)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, tagView{Tag: tag})
}

// deleteTagHandler 处理 DELETE /api/tags/:id。
//
// 删除标注不会动邮箱,只把它从所有别名上摘掉。
func (s *Server) deleteTagHandler(c *gin.Context) {
	affected, err := s.meta.DeleteTag(c.Param("id"))
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, gin.H{"deleted": true, "affected": affected})
}

type aliasTagsReq struct {
	Email  string   `json:"email"`
	TagIDs []string `json:"tag_ids"`
}

// setAliasTagsHandler 处理 PUT /api/alias-tags。
//
// 覆盖语义:传空数组表示把这个别名上的标注全部摘掉。
func (s *Server) setAliasTagsHandler(c *gin.Context) {
	var req aliasTagsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "email 必填")
		return
	}
	entry, err := s.meta.SetAliasTags(req.Email, req.TagIDs)
	if err != nil {
		mapMetaErr(c, err)
		return
	}
	ok(c, entry)
}
