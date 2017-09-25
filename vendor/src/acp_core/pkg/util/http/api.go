package http

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/net/context"

	"github.com/go-chi/render"

	"github.com/govalidator/validator"
)

//ValidatorStructRequest 验证请求数据
//data 传入指针
func ValidatorStructRequest(r *http.Request, data interface{}, message validator.MapData) url.Values {
	opts := validator.Options{
		Request: r,
		Data:    data,
	}
	if message != nil {
		opts.Messages = message
	}
	v := validator.New(opts)
	result := v.ValidateStructJSON()
	return result
}

//ValidatorMapRequest 验证请求数据从map
func ValidatorMapRequest(r *http.Request, rule validator.MapData, message validator.MapData) (map[string]interface{}, url.Values) {
	data := make(map[string]interface{}, 0)
	opts := validator.Options{
		Request: r,
		Data:    &data,
	}
	if rule != nil {
		opts.Rules = rule
	}
	if message != nil {
		opts.Messages = message
	}
	vd := validator.New(opts)
	e := vd.ValidateMapJSON()
	return data, e
}

//ValidatorRequestStructAndErrorResponse 验证并格式化请求数据为对象
// retrun true 继续执行
// return false 参数错误，终止
func ValidatorRequestStructAndErrorResponse(r *http.Request, w http.ResponseWriter, data interface{}, message validator.MapData) bool {
	if re := ValidatorStructRequest(r, data, message); len(re) > 0 {
		ReturnValidationError(r, w, re)
		return false
	}
	return true
}

//ValidatorRequestMapAndErrorResponse 验证并格式化请求数据为对象
// retrun true 继续执行
// return false 参数错误，终止
func ValidatorRequestMapAndErrorResponse(r *http.Request, w http.ResponseWriter, rule validator.MapData, messgae validator.MapData) (map[string]interface{}, bool) {
	data, re := ValidatorMapRequest(r, rule, messgae)
	if len(re) > 0 {
		ReturnValidationError(r, w, re)
		return nil, false
	}
	return data, true
}

//ResponseBody api返回数据格式
type ResponseBody struct {
	ValidationError url.Values    `json:"validation_error,omitempty"`
	Msg             string        `json:"msg,omitempty"`
	Bean            interface{}   `json:"bean,omitempty"`
	List            []interface{} `json:"list,omitempty"`
	//数据集总数
	ListAllNumber int `json:"number,omitempty"`
	//当前页码数
	Page int `json:"page,omitempty"`
}

//ParseResponseBody 解析成ResponseBody
func ParseResponseBody(red io.ReadCloser, dataType string) (re ResponseBody, err error) {
	if red == nil {
		err = errors.New("readcloser can not be nil")
		return
	}
	defer red.Close()
	switch render.GetContentType(dataType) {
	case render.ContentTypeJSON:
		err = render.DecodeJSON(red, &re)
	case render.ContentTypeXML:
		err = render.DecodeXML(red, &re)
	// case ContentTypeForm: // TODO
	default:
		err = errors.New("render: unable to automatically decode the request content type")
	}
	return
}

//ReturnValidationError 参数错误返回
func ReturnValidationError(r *http.Request, w http.ResponseWriter, err url.Values) {
	r = r.WithContext(context.WithValue(r.Context(), render.StatusCtxKey, http.StatusBadRequest))
	render.DefaultResponder(w, r, ResponseBody{ValidationError: err})
}

//ReturnSuccess 成功返回
func ReturnSuccess(r *http.Request, w http.ResponseWriter, datas ...interface{}) {
	r = r.WithContext(context.WithValue(r.Context(), render.StatusCtxKey, http.StatusOK))
	if len(datas) == 0 {
		render.DefaultResponder(w, r, ResponseBody{})
		return
	}
	if len(datas) == 1 {
		render.DefaultResponder(w, r, ResponseBody{Bean: datas[0]})
		return
	}
	if len(datas) > 1 {
		render.DefaultResponder(w, r, ResponseBody{List: datas})
		return
	}
}

//ReturnList 返回列表
func ReturnList(r *http.Request, w http.ResponseWriter, listAllNumber, page int, datas ...interface{}) {
	r = r.WithContext(context.WithValue(r.Context(), render.StatusCtxKey, http.StatusOK))
	render.DefaultResponder(w, r, ResponseBody{List: datas, ListAllNumber: listAllNumber, Page: page})
}

//ReturnError 返回错误信息
func ReturnError(r *http.Request, w http.ResponseWriter, code int, msg string) {
	r = r.WithContext(context.WithValue(r.Context(), render.StatusCtxKey, code))
	render.DefaultResponder(w, r, ResponseBody{Msg: msg})
}

//Return  自定义
func Return(r *http.Request, w http.ResponseWriter, code int, reb ResponseBody) {
	r = r.WithContext(context.WithValue(r.Context(), render.StatusCtxKey, code))
	render.DefaultResponder(w, r, reb)
}
