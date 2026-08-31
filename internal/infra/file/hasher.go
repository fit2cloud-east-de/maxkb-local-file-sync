package file

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
)

// CalculateMD5 流式计算文件的 MD5 哈希值
func CalculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CalculateMD5Reader 从 Reader 计算 MD5
func CalculateMD5Reader(reader io.Reader) (string, error) {
	hash := md5.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// FileSnapshot 文件快照
type FileSnapshot struct {
	Path       string
	Size       int64
	ModifiedAt int64 // Unix timestamp
	MD5        string
}

// CreateSnapshot 创建文件快照
func CreateSnapshot(filePath string) (*FileSnapshot, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	md5Hash, err := CalculateMD5(filePath)
	if err != nil {
		return nil, err
	}

	return &FileSnapshot{
		Path:       filePath,
		Size:       stat.Size(),
		ModifiedAt: stat.ModTime().Unix(),
		MD5:        md5Hash,
	}, nil
}

// Validate 验证文件是否发生变化
func (fs *FileSnapshot) Validate() (bool, error) {
	stat, err := os.Stat(fs.Path)
	if err != nil {
		return false, err
	}

	// 快速检查：大小或修改时间改变则文件已变化
	if stat.Size() != fs.Size || stat.ModTime().Unix() != fs.ModifiedAt {
		return false, nil
	}

	// 大小和修改时间相同，进一步校验 MD5
	currentMD5, err := CalculateMD5(fs.Path)
	if err != nil {
		return false, err
	}

	return currentMD5 == fs.MD5, nil
}
