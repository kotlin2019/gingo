package api

var api = `package v1

import (
	"fmt"
	"{{.Appname}}/global"
	"{{.Appname}}/global/resp"
	"{{.Appname}}/middleware"
	"{{.Appname}}/model"
	"{{.Appname}}/model/request"
	"{{.Appname}}/model/response"
	"{{.Appname}}/service"
	"{{.Appname}}/utils"
	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
	"mime/multipart"
	"time"
)

func Register(c *gin.Context) {
	var R request.RegisterStruct
	_ = c.ShouldBindJSON(&R)
	UserVerify := utils.Rules{
		"Username":    {utils.NotEmpty()},
		"NickName":    {utils.NotEmpty()},
		"Password":    {utils.NotEmpty()},
		"AuthorityId": {utils.NotEmpty()},
	}
	UserVerifyErr := utils.Verify(R, UserVerify)
	if UserVerifyErr != nil {
		response.FailWithMessage(UserVerifyErr.Error(), c)
		return
	}
	user := &model.SysUser{Username: R.Username, NickName: R.NickName, Password: R.Password, HeaderImg: R.HeaderImg, AuthorityId: R.AuthorityId}
	err, userReturn := service.Register(*user)
	if err != nil {
		response.FailWithDetailed(response.ERROR, resp.SysUserResponse{User: userReturn}, fmt.Sprintf("%v", err), c)
	} else {
		response.OkDetailed(resp.SysUserResponse{User: userReturn}, "注册成功", c)
	}
}

func Login(c *gin.Context) {
	var L request.RegisterAndLoginStruct
	_ = c.ShouldBindJSON(&L)
	UserVerify := utils.Rules{
		"CaptchaId": {utils.NotEmpty()},
		"Captcha":   {utils.NotEmpty()},
		"Username":  {utils.NotEmpty()},
		"Password":  {utils.NotEmpty()},
	}
	UserVerifyErr := utils.Verify(L, UserVerify)
	if UserVerifyErr != nil {
		response.FailWithMessage(UserVerifyErr.Error(), c)
		return
	}
	if store.Verify(L.CaptchaId, L.Captcha, true) {
		U := &model.SysUser{Username: L.Username, Password: L.Password}
		if err, user := service.Login(U); err != nil {
			response.FailWithMessage(fmt.Sprintf("用户名密码错误或%v", err), c)
		} else {
			tokenNext(c, *user)
		}
	} else {
		response.FailWithMessage("验证码错误", c)
	}

}



// 登录以后签发jwt
func tokenNext(c *gin.Context, user model.SysUser) {
	j := &middleware.JWT{
		SigningKey: []byte(global.GVA_CONFIG.JWT.SigningKey), // 唯一签名
	}
	clams := request.CustomClaims{
		UUID:        user.UUID,
		ID:          user.ID,
		NickName:    user.NickName,
		Username:    user.Username,
		AuthorityId: user.AuthorityId,
		BufferTime:  60*60*24, // 缓冲时间1天 缓冲时间内会获得新的token刷新令牌 此时一个用户会存在两个有效令牌 但是前端只留一个 另一个会丢失
		StandardClaims: jwt.StandardClaims{
			NotBefore: time.Now().Unix() - 1000,       // 签名生效时间
			ExpiresAt: time.Now().Unix() + 60*60*24*7, // 过期时间 7天
			Issuer:    "qmPlus",                       // 签名的发行者
		},
	}
	token, err := j.CreateToken(clams)
	if err != nil {
		response.FailWithMessage("获取token失败", c)
		return
	}
	if !global.GVA_CONFIG.System.UseMultipoint {
		response.OkWithData(resp.LoginResponse{
			User:      user,
			Token:     token,
			ExpiresAt: clams.StandardClaims.ExpiresAt * 1000,
		}, c)
		return
	}
	err, jwtStr := service.GetRedisJWT(user.Username)
	if err == redis.Nil {
		if err := service.SetRedisJWT(token, user.Username); err != nil {
			response.FailWithMessage("设置登录状态失败", c)
			return
		}
		response.OkWithData(resp.LoginResponse{
			User:      user,
			Token:     token,
			ExpiresAt: clams.StandardClaims.ExpiresAt * 1000,
		}, c)
	} else if err != nil {
		response.FailWithMessage(fmt.Sprintf("%v", err), c)
	} else {
		var blackJWT model.JwtBlacklist
		blackJWT.Jwt = jwtStr
		if err := service.JsonInBlacklist(blackJWT); err != nil {
			response.FailWithMessage("jwt作废失败", c)
			return
		}
		if err := service.SetRedisJWT(jwtStr, user.Username); err != nil {
			response.FailWithMessage("设置登录状态失败", c)
			return
		}
		response.OkWithData(resp.LoginResponse{
			User:      user,
			Token:     token,
			ExpiresAt: clams.StandardClaims.ExpiresAt * 1000,
		}, c)
	}
}

func ChangePassword(c *gin.Context) {
	var params request.ChangePasswordStruct
	_ = c.ShouldBindJSON(&params)
	UserVerify := utils.Rules{
		"Username":    {utils.NotEmpty()},
		"Password":    {utils.NotEmpty()},
		"NewPassword": {utils.NotEmpty()},
	}
	UserVerifyErr := utils.Verify(params, UserVerify)
	if UserVerifyErr != nil {
		response.FailWithMessage(UserVerifyErr.Error(), c)
		return
	}
	U := &model.SysUser{Username: params.Username, Password: params.Password}
	if err, _ := service.ChangePassword(U, params.NewPassword); err != nil {
		response.FailWithMessage("修改失败，请检查用户名密码", c)
	} else {
		response.OkWithMessage("修改成功", c)
	}
}

func UploadHeaderImg(c *gin.Context) {
	claims, _ := c.Get("claims")
	// 获取头像文件
	// 这里我们通过断言获取 claims内的所有内容
	waitUse := claims.(*request.CustomClaims)
	uuid := waitUse.UUID
	_, header, err := c.Request.FormFile("headerImg")
	// 便于找到用户 以后从jwt中取
	if err != nil {
		response.FailWithMessage(fmt.Sprintf("上传文件失败，%v", err), c)
	} else {
		// 文件上传后拿到文件路径
		var uploadErr error
		var filePath string
		if global.GVA_CONFIG.LocalUpload.Local {
			// 本地上传
			uploadErr, filePath, _ = utils.UploadAvatarLocal(header)
		} else {
			// 七牛云上传
			uploadErr, filePath, _ = utils.UploadRemote(header)
		}
		if uploadErr != nil {
			response.FailWithMessage(fmt.Sprintf("接收返回值失败，%v", err), c)
		} else {
			// 修改数据库后得到修改后的user并且返回供前端使用
			err, user := service.UploadHeaderImg(uuid, filePath)
			if err != nil {
				response.FailWithMessage(fmt.Sprintf("修改数据库链接失败，%v", err), c)
			} else {
				response.OkWithData(resp.SysUserResponse{User: *user}, c)
			}
		}
	}
}

func GetUserList(c *gin.Context) {
	var pageInfo request.PageInfo
	_ = c.ShouldBindJSON(&pageInfo)
	PageVerifyErr := utils.Verify(pageInfo, utils.CustomizeMap["PageVerify"])
	if PageVerifyErr != nil {
		response.FailWithMessage(PageVerifyErr.Error(), c)
		return
	}
	err, list, total := service.GetUserInfoList(pageInfo)
	if err != nil {
		response.FailWithMessage(fmt.Sprintf("获取数据失败，%v", err), c)
	} else {
		response.OkWithData(resp.PageResult{
			List:     list,
			Total:    total,
			Page:     pageInfo.Page,
			PageSize: pageInfo.PageSize,
		}, c)
	}
}

func SetUserAuthority(c *gin.Context) {
	var sua request.SetUserAuth
	_ = c.ShouldBindJSON(&sua)
	UserVerify := utils.Rules{
		"UUID":        {utils.NotEmpty()},
		"AuthorityId": {utils.NotEmpty()},
	}
	UserVerifyErr := utils.Verify(sua, UserVerify)
	if UserVerifyErr != nil {
		response.FailWithMessage(UserVerifyErr.Error(), c)
		return
	}
	err := service.SetUserAuthority(sua.UUID, sua.AuthorityId)
	if err != nil {
		response.FailWithMessage(fmt.Sprintf("修改失败，%v", err), c)
	} else {
		response.OkWithMessage("修改成功", c)
	}
}

func DeleteUser(c *gin.Context) {
	var reqId request.GetById
	_ = c.ShouldBindJSON(&reqId)
	IdVerifyErr := utils.Verify(reqId, utils.CustomizeMap["IdVerify"])
	if IdVerifyErr != nil {
		response.FailWithMessage(IdVerifyErr.Error(), c)
		return
	}
	err := service.DeleteUser(reqId.Id)
	if err != nil {
		response.FailWithMessage(fmt.Sprintf("删除失败，%v", err), c)
	} else {
		response.OkWithMessage("删除成功", c)
	}
}
`
