// Package crypto 提供加密、解密、签名、验签等功能
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

// 加密过程：
//  1、处理数据，对数据进行填充，采用PKCS7（当密钥长度不够时，缺几位补几个几）的方式。
//  2、对数据进行加密，采用AES加密方法中CBC加密模式
//  3、16/24/32位字符串，分别对应AES-128，AES-192，AES-256加密方法
// 解密过程相反

// TestKey is the key for testing
var (
	TestKey = []byte("0123456789ABCDEF")
)

// pkcs7Padding 填充
func pkcs7Padding(data []byte, blockSize int) []byte {
	// 判断缺少几位长度。最少1，最多 blockSize
	padding := blockSize - len(data)%blockSize
	// 补足位数。把切片[]byte{byte(padding)}复制padding个
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

// pkcs7UnPadding 填充的反向操作
func pkcs7UnPadding(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, errors.New("invalid padding input")
	}
	// 获取填充的个数
	unPadding := int(data[length-1])
	if unPadding < 0 || unPadding > length {
		return nil, errors.New("invalid padding input")
	}
	return data[:(length - unPadding)], nil
}

// AesEncrypt 加密
func AesEncrypt(data []byte, key []byte) ([]byte, error) {
	// 创建加密实例
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	// 判断加密快的大小
	blockSize := block.BlockSize()
	// 填充
	paddingBytes := pkcs7Padding(data, blockSize)
	// 初始化加密数据接收切片
	encrypted := make([]byte, len(paddingBytes))
	// 使用cbc加密模式
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	// 执行加密
	blockMode.CryptBlocks(encrypted, paddingBytes)
	return encrypted, nil
}

// AesDecrypt 解密
func AesDecrypt(data []byte, key []byte) ([]byte, error) {
	// 创建实例
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// 获取块的大小
	blockSize := block.BlockSize()
	// 使用cbc
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	// 初始化解密数据接收切片
	decrypted := make([]byte, len(data))
	// 执行解密
	blockMode.CryptBlocks(decrypted, data)
	// 去除填充
	decrypted, err = pkcs7UnPadding(decrypted)
	if err != nil {
		return nil, err
	}

	return decrypted, nil
}

// EncryptByAes Aes加密 后 base64 再加
func EncryptByAes(data []byte) (string, error) {
	res, err := AesEncrypt(data, TestKey)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(res), nil
}

// DecryptByAes Aes 解密
func DecryptByAes(data string) ([]byte, error) {
	dataByte, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}

	return AesDecrypt(dataByte, TestKey)
}

// GenRandomKey generates a random key and returns it as base64 std encoding string
func GenRandomKey(keyLen int) (string, error) {
	switch keyLen {
	case 16, 24, 32:
		key := make([]byte, keyLen)
		_, err := rand.Read(key)
		if err != nil {
			return "", err
		}

		return base64.StdEncoding.EncodeToString(key), nil
	default:
		return "", errors.New("unsupported length")
	}
}

// ParseKey parses a string key into key bytes,
// if the key is of valid length, use it directly,
// otherwise try to decode it as a base64 std encoding string
func ParseKey(key string) ([]byte, error) {
	keyLen := len(key)
	switch keyLen {
	case 0:
		return nil, errors.New("empty key")
	case 16, 24, 32:
		return []byte(key), nil
	default:
		return base64.StdEncoding.DecodeString(key)
	}
}
