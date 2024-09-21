package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
)

var mu sync.Mutex

const expire_time = 10
const local_ip = "192.168.100.100"

// 定义一个结构体来表示数据模型
type UUIDItem struct {
	UUID       string `json:"uuid"`
	IP         string `json:"ip"`
	UserID     string `json:"user_id"`
	ExpireTime int64  `json:"expire_time"`
}

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
		if time.Now().Unix()-item.ExpireTime > expire_time {
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
		UUID:       uuid,
		IP:         c.ClientIP(),
		UserID:     "",
		ExpireTime: time.Now().Unix(),
	}
	m.items = append(m.items, item)
	mu.Lock()
	m.saveItemsToFile()
	mu.Unlock()
	codeImg, _ := qrcode.Encode("http://"+local_ip+":3000/phone?uuid="+uuid, qrcode.Medium, 256)
	return codeImg, uuid
}

// 生成 UUID
func (m *UUIDManager) generateUUID() string {
	node, _ := snowflake.NewNode(1)
	return node.Generate().String()
}

// 检查 UUID
func (m *UUIDManager) checkUUID(uuid string) (response, error) {
	item, err := m.searchUUID(uuid)
	if err != nil {
		return response{
			Success: false,
			UserID:  uuid,
			Message: "notfound",
		}, err
	}
	if time.Now().Unix()-item.ExpireTime > expire_time {
		m.delItemInItem(uuid)
		return response{
			Success: false,
			UserID:  uuid,
			Message: "expired",
		}, nil
	}
	if item.UserID == "" {
		return response{
			Success: false,
			UserID:  uuid,
			Message: "notyet",
		}, nil
	}
	return response{
		Success: true,
		UserID:  item.UserID,
		Message: "success",
	}, nil
}

type response struct {
	Success bool   `json:"success"`
	UserID  string `json:"user_id"`
	Message string `json:"message"`
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

		// 解析请求体
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Invalid request",
			})
			return
		}

		// 检查 UUID
		res, err := manager.checkUUID(reqBody.UUID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": res.Success,
			"user_id": res.UserID,
			"message": res.Message,
		})

		// 删除 UUID
		if res.Success {
			manager.delItemInItem(reqBody.UUID)
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
		res, err := manager.checkUUID(reqBody.UUID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}

		// 保存用户 ID
		if res.Success {
			for itemIndex, item := range manager.items {
				if item.UUID == reqBody.UUID {
					manager.items[itemIndex].UserID = reqBody.UserID
					break
				}
			}
			mu.Lock()
			manager.saveItemsToFile()
			mu.Unlock()
		}

		c.JSON(http.StatusOK, gin.H{
			"success": res.Success,
			"user_id": res.UserID,
			"message": res.Message,
		})
	})
	r.Run("0.0.0.0:8080")
}
