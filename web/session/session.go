package session

import (
	"encoding/gob"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"x-ui/database/model"
)

const (
	loginUser = "LOGIN_USER"
)

func init() {
	gob.Register(model.User{})
}

// SetLoginUser 写入登录态。
// 为避免 session cookie 承载密码哈希，这里只保留 Id / Username，
// 校验旧密码等需求统一走 UserService.CheckUser 走 bcrypt 路径。
func SetLoginUser(c *gin.Context, user *model.User) error {
	s := sessions.Default(c)
	sanitized := model.User{
		Id:       user.Id,
		Username: user.Username,
	}
	s.Set(loginUser, sanitized)
	return s.Save()
}

func GetLoginUser(c *gin.Context) *model.User {
	s := sessions.Default(c)
	obj := s.Get(loginUser)
	if obj == nil {
		return nil
	}
	user := obj.(model.User)
	return &user
}

func IsLogin(c *gin.Context) bool {
	return GetLoginUser(c) != nil
}

// ClearSession 清空登录态并让浏览器立即删除 session cookie。
//
// Path 必须与登录时下发 cookie 所用的 basePath 完全一致：
// 浏览器按 (name, domain, path) 三元组定位 cookie，历史实现固定写 "/"，
// 在自定义 basePath（例如 /panel/）部署下删除的是一个根本不存在的 cookie，
// 真正的 session cookie 原封不动地留在浏览器里，登出形同虚设。
func ClearSession(c *gin.Context) {
	s := sessions.Default(c)
	s.Clear()
	path := c.GetString("base_path")
	if path == "" {
		path = "/"
	}
	s.Options(sessions.Options{
		Path:     path,
		HttpOnly: true,
		MaxAge:   -1,
	})
	s.Save()
}
