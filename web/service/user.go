package service

import (
	"errors"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/logger"

	"gorm.io/gorm"
)

type UserService struct {
}

func (s *UserService) GetFirstUser() (*model.User, error) {
	db := database.GetDB()

	user := &model.User{}
	err := db.Model(model.User{}).
		First(user).
		Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// CheckUser 按 (username, password) 校验用户凭证。
//
// 兼容两种存储形态：
//  1. bcrypt 哈希（v1.0.0+ 新格式）
//  2. 明文密码（v1.0.0 之前遗留数据）
//
// 当校验命中明文密码时，本函数会自动将其升级为 bcrypt 哈希落库，
// 实现用户无感的平滑迁移。升级失败不影响本次登录结果，仅记录 warning。
func (s *UserService) CheckUser(username string, password string) *model.User {
	db := database.GetDB()

	user := &model.User{}
	err := db.Model(model.User{}).
		Where("username = ?", username).
		First(user).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil
	} else if err != nil {
		logger.Warning("check user err:", err)
		return nil
	}

	ok, needUpgrade := VerifyPassword(user.Password, password)
	if !ok {
		return nil
	}

	if needUpgrade {
		hashed, herr := HashPassword(password)
		if herr != nil {
			logger.Warning("hash password for upgrade failed:", herr)
		} else if uerr := db.Model(model.User{}).
			Where("id = ?", user.Id).
			Update("password", hashed).Error; uerr != nil {
			logger.Warning("persist upgraded password hash failed:", uerr)
		} else {
			user.Password = hashed
			logger.Infof("migrated plaintext password of user #%d to bcrypt", user.Id)
		}
	}
	return user
}

func (s *UserService) UpdateUser(id int, username string, password string) error {
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	db := database.GetDB()
	if err := db.Model(model.User{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"username": username,
			"password": hashed,
		}).
		Error; err != nil {
		return err
	}
	// 用户已自行设置口令，清除"仍在使用首启随机口令"的告警标记。
	if err := database.MarkInitialCredentials(false); err != nil {
		logger.Warning("clear initial credentials flag failed:", err)
	}
	return nil
}

// UsingInitialCredentials 返回当前管理员口令是否仍是首次启动自动生成的随机值。
// 面板据此在入站列表页顶部显示提醒；读取失败时保守返回 false（不打扰用户）。
func (s *UserService) UsingInitialCredentials() bool {
	db := database.GetDB()
	if db == nil {
		return false
	}
	setting := &model.Setting{}
	err := db.Model(model.Setting{}).
		Where("key = ?", database.SettingKeyInitialCredentials).
		First(setting).Error
	if err != nil {
		return false
	}
	return setting.Value == "true"
}

func (s *UserService) UpdateFirstUser(username string, password string) error {
	if username == "" {
		return errors.New("username can not be empty")
	} else if password == "" {
		return errors.New("password can not be empty")
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	db := database.GetDB()
	user := &model.User{}
	err = db.Model(model.User{}).First(user).Error
	if database.IsNotFound(err) {
		user.Username = username
		user.Password = hashed
		return db.Model(model.User{}).Create(user).Error
	} else if err != nil {
		return err
	}
	user.Username = username
	user.Password = hashed
	if err := db.Save(user).Error; err != nil {
		return err
	}
	if err := database.MarkInitialCredentials(false); err != nil {
		logger.Warning("clear initial credentials flag failed:", err)
	}
	return nil
}
