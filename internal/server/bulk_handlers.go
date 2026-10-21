package server

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 别名批量操作接口。
//
// 邮箱管理页勾选多条后,前端只发一个请求,由服务端逐条执行并汇总结果,
// 这样部分失败也能逐条回显(哪几个没成功、为什么),而不是整体报错。
//
// 分工:
//   - deactivate / reactivate / delete 落到 iCloud,必须一条一条调上游;
//   - assign_group 只改本地元数据,整批一次落盘。
const (
	bulkMaxItems = 200 // 单次批量上限,再大就该分页处理了
	bulkWorkers  = 4   // 同时打到 iCloud 的并发数,太高容易触发限流
)

// bulkAliasItem 是批量操作里的单个目标。删除时带上 email,
// 便于同时清理本地分组记录。
type bulkAliasItem struct {
	AnonymousID string `json:"anonymous_id"`
	Email       string `json:"email"`
}

type bulkAliasReq struct {
	AccountID string          `json:"account_id"`
	Action    string          `json:"action"`
	Aliases   []bulkAliasItem `json:"aliases"`
	// GroupID 仅 assign_group 使用;空串表示移出分组。
	GroupID string `json:"group_id"`
}

// bulkItemResult 是单个目标的执行结果。
type bulkItemResult struct {
	AnonymousID string `json:"anonymous_id,omitempty"`
	Email       string `json:"email,omitempty"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
}

// bulkAliasHandler 处理 POST /api/alias-bulk。
func (s *Server) bulkAliasHandler(c *gin.Context) {
	var req bulkAliasReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 请求体不是合法 JSON")
		return
	}
	if strings.TrimSpace(req.AccountID) == "" {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: account_id 必填")
		return
	}
	if len(req.Aliases) == 0 {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: aliases 不能为空")
		return
	}
	if len(req.Aliases) > bulkMaxItems {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "单次最多处理 200 个邮箱")
		return
	}
	req.AccountID = strings.TrimSpace(req.AccountID)

	switch req.Action {
	case "deactivate", "reactivate", "delete", "assign_group":
	default:
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: action 取值无效")
		return
	}

	// 归组不动 iCloud,只写本地文件,是唯一可以整批一次完成的操作。
	if req.Action == "assign_group" {
		emails := make([]string, 0, len(req.Aliases))
		for _, item := range req.Aliases {
			if strings.TrimSpace(item.Email) == "" {
				failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "参数错误: 归入分组需要 email")
				return
			}
			emails = append(emails, item.Email)
		}
		applied, err := s.meta.AssignGroupBulk(emails, req.AccountID, req.GroupID)
		if err != nil {
			mapMetaErr(c, err)
			return
		}
		results := make([]bulkItemResult, 0, len(req.Aliases))
		for _, item := range req.Aliases {
			results = append(results, bulkItemResult{
				AnonymousID: item.AnonymousID,
				Email:       item.Email,
				OK:          true,
			})
		}
		ok(c, gin.H{
			"action":    req.Action,
			"group_id":  req.GroupID,
			"total":     len(results),
			"applied":   applied,
			"succeeded": len(results),
			"failed":    0,
			"results":   results,
		})
		return
	}

	// 只有删除需要顺带清理本地元数据:停用/激活只是改 iCloud 状态,
	// 别名和它的分组记录都还在。
	var onSuccess func(bulkAliasItem)
	if req.Action == "delete" {
		onSuccess = func(item bulkAliasItem) {
			// 清理失败无所谓:这是附加动作,不影响删除本身的结果
			if strings.TrimSpace(item.Email) != "" {
				_, _ = s.meta.DeleteEntry(item.Email)
			}
		}
	}

	results := runBulkUpstream(req, func(item bulkAliasItem) error {
		switch req.Action {
		case "deactivate":
			_, err := s.be.SetAliasActive(req.AccountID, item.AnonymousID, false)
			return err
		case "reactivate":
			_, err := s.be.SetAliasActive(req.AccountID, item.AnonymousID, true)
			return err
		default: // delete
			return s.be.DeleteAlias(req.AccountID, item.AnonymousID)
		}
	}, onSuccess)

	succeeded := 0
	for _, res := range results {
		if res.OK {
			succeeded++
		}
	}
	ok(c, gin.H{
		"action":    req.Action,
		"total":     len(results),
		"applied":   succeeded,
		"succeeded": succeeded,
		"failed":    len(results) - succeeded,
		"results":   results,
	})
}

// runBulkUpstream 并发执行逐条上游调用,保持结果顺序与请求一致。
// onSuccess 在单条成功后调用(仅用于本地副作用清理)。
func runBulkUpstream(req bulkAliasReq, call func(bulkAliasItem) error, onSuccess func(bulkAliasItem)) []bulkItemResult {
	results := make([]bulkItemResult, len(req.Aliases))
	sem := make(chan struct{}, bulkWorkers)
	var wg sync.WaitGroup

	for i, item := range req.Aliases {
		if strings.TrimSpace(item.AnonymousID) == "" {
			results[i] = bulkItemResult{Email: item.Email, OK: false, Error: "缺少 anonymous_id"}
			continue
		}
		wg.Add(1)
		go func(idx int, item bulkAliasItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := call(item); err != nil {
				results[idx] = bulkItemResult{
					AnonymousID: item.AnonymousID,
					Email:       item.Email,
					OK:          false,
					Error:       asBackendError(err).Message,
				}
				return
			}
			if onSuccess != nil {
				onSuccess(item)
			}
			results[idx] = bulkItemResult{
				AnonymousID: item.AnonymousID,
				Email:       item.Email,
				OK:          true,
			}
		}(i, item)
	}
	wg.Wait()
	return results
}
