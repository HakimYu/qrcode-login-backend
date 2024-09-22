package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
)

// 进程锁
var mu sync.Mutex

// 过期时间(单位：秒)
const expire_time = 6000

// 填写前端域名(IP+端口)
const domain = "192.168.100.100:3000"

// 定义一个结构体来表示数据模型
type UUIDItem struct {
	UUID         string `json:"uuid"`
	IP           string `json:"ip"`
	UserID       string `json:"user_id"`
	GenerateTime int64  `json:"generate_time"`
}

// 获取本地 IP(供使用者查看)
func GetLocalIP() ([]string, error) {
	var localIPs []string

	// 获取所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		// 过滤掉未激活的接口和回环接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// 获取接口的地址
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}

		for _, addr := range addrs {
			// 检查地址类型
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.IsPrivate() {
				localIPs = append(localIPs, ipNet.IP.String())
			}
		}
	}

	return localIPs, nil
}

// UUIDManager 结构体
type UUIDManager struct {
	items []UUIDItem
}

// NewUUIDManager 创建一个新的 UUIDManager
func NewUUIDManager() *UUIDManager {
	manager := &UUIDManager{}
	manager.readItemsFromFile()
	return manager
}

// 从文件读取数据
func (m *UUIDManager) readItemsFromFile() {
	data, err := os.ReadFile("UUID.json")
	if err != nil {
		log.Println("Failed to open file: UUID.json", err)
		return
	}
	err = json.Unmarshal(data, &m.items)
	if err != nil {
		log.Println("Failed to unmarshal JSON: UUID.json", err)
	}
}

// 查找 UUID
func (m *UUIDManager) searchUUID(uuid string) (UUIDItem, error) {
	for _, item := range m.items {
		if item.UUID == uuid {
			return item, nil
		}
	}
	return UUIDItem{}, fmt.Errorf("UUID not found")
}

// 保存数据到文件
func (m *UUIDManager) saveItemsToFile() {
	data, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		log.Println("Failed to marshal JSON", err)
		return
	}
	err = os.WriteFile("UUID.json", data, 0644)
	if err != nil {
		log.Println("Failed to write file: UUID.json", err)
	}
}

// 删除 UUID
func (m *UUIDManager) delItemInItem(uuid string) {
	for index, item := range m.items {
		if item.UUID == uuid {
			m.items = append(m.items[:index], m.items[index+1:]...)
			break
		}
	}
}

// 生成二维码
func (m *UUIDManager) getCodeImg(c *gin.Context) ([]byte, string) {
	mu.Lock()
	m.readItemsFromFile()
	var toDelete []string
	for _, item := range m.items {
		if time.Now().Unix()-item.GenerateTime > expire_time {
			fmt.Printf("Found expired uuid: %s, delete it\n", item.UUID)
			toDelete = append(toDelete, item.UUID)
		}
	}
	for _, uuid := range toDelete {
		m.delItemInItem(uuid)
	}
	m.saveItemsToFile()
	mu.Unlock()
	uuid := m.generateUUID()
	item := UUIDItem{
		UUID:         uuid,
		IP:           c.ClientIP(),
		UserID:       "",
		GenerateTime: time.Now().Unix(),
	}
	m.items = append(m.items, item)
	mu.Lock()
	m.saveItemsToFile()
	mu.Unlock()
	baseURL := fmt.Sprintf("http://%s/phone", domain)
	params := url.Values{}
	params.Add("uuid", uuid)
	params.Add("ip", c.ClientIP())
	fullURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())
	codeImg, _ := qrcode.Encode(fullURL, qrcode.Medium, 256)
	return codeImg, uuid
}

// 生成 UUID
func (m *UUIDManager) generateUUID() string {
	node, _ := snowflake.NewNode(1)
	return node.Generate().String()
}

type checkResponse struct {
	Success bool   `json:"success"`
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

// 检查 UUID
func (m *UUIDManager) checkUUID(uuid string) (checkResponse, error) {

	m.readItemsFromFile()
	item, err := m.searchUUID(uuid)
	fmt.Printf("item: %v\n", item)
	if err != nil {
		return checkResponse{
			Success: false,
			UserID:  "",
			Message: "notfound",
		}, err
	}
	if time.Now().Unix()-item.GenerateTime > expire_time {
		m.delItemInItem(uuid)
		return checkResponse{
			Success: false,
			UserID:  "",
			Message: "expired",
		}, nil
	}
	if item.UserID == "" {
		return checkResponse{
			Success: false,
			UserID:  item.UserID,
			Message: "notyet",
		}, nil
	}
	fmt.Printf("success")
	m.delItemInItem(uuid)
	return checkResponse{
		Success: true,
		UserID:  item.UserID,
		Message: "success",
	}, nil
}

type loginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (m *UUIDManager) login(uuid string, user_id string) (loginResponse, error) {
	mu.Lock()
	m.readItemsFromFile()
	mu.Unlock()
	for index, item := range m.items {
		if item.UUID == uuid {
			fmt.Printf("found uuid: %s, user_id: %s\n", item.UUID, item.UserID)
			fmt.Printf("user_id: %s\n", user_id)
			m.items[index].UserID = user_id
			mu.Lock()
			m.saveItemsToFile()
			mu.Unlock()
			return loginResponse{
				Success: true,
				Message: "success",
			}, nil
		}
	}
	return loginResponse{
		Success: false,
		Message: "notfound",
	}, fmt.Errorf("UUID not found")
}

func main() {
	ips, err := GetLocalIP()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	for _, ip := range ips {
		fmt.Printf("local ip: %v\n", ip)
	}
	manager := NewUUIDManager()
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},                             // 允许的源
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},        // 允许的方法
		AllowHeaders:     []string{"Content-Type", "Authorization"}, // 允许的头部
		ExposeHeaders:    []string{"uuid"},
		AllowCredentials: true, // 允许携带凭据
	}))
	r.GET("/getqrcode", func(c *gin.Context) {
		img, uuid := manager.getCodeImg(c)
		c.Header("Access-Control-Expose-Headers", "uuid")
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("uuid", uuid)
		c.Header("Content-Type", "image/png")
		c.Data(http.StatusOK, "image/png", img)
	})

	r.POST("/checkuuid", func(c *gin.Context) {
		var reqBody struct {
			UUID string `json:"uuid"`
		}

		// 请求错误
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Invalid request",
			})
			return
		}

		// 检查 UUID
		res, err := manager.checkUUID(reqBody.UUID)
		//检查出错
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": res.Message,
			})
			return
		}

		c.JSON(http.StatusOK, res)

		// 删除 UUID
		if res.Success {
			manager.delItemInItem(reqBody.UUID)
			mu.Lock()
			manager.saveItemsToFile()
			mu.Unlock()
		}
	})
	r.POST("/login", func(c *gin.Context) {
		var reqBody struct {
			UUID   string `json:"uuid"`
			UserID string `json:"user_id"`
		}

		// 解析请求体
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Invalid request",
			})
			return
		}
		// 检查 UUID
		res, err := manager.login(reqBody.UUID, reqBody.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, res)
	})
	r.Run("0.0.0.0:8099")
}
