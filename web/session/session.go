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

func ClearSession(c *gin.Context) {
	s := sessions.Default(c)
	s.Clear()
	s.Options(sessions.Options{
		Path:   "/",
		MaxAge: -1,
	})
	s.Save()
}
