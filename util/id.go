package util

import (
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"github.com/zentures/cityhash"
)

var (
	snowflakeOnce sync.Once
	snowflakeNode *snowflake.Node
	snowflakeErr  error
)

func newSnowFlakeNode() (*snowflake.Node, error) {
	ip, err := GetOneIP()
	if err != nil {
		return nil, err
	}

	id := IPtoUInt(ip)
	node, err := snowflake.NewNode(int64(id % 1024))
	if err != nil {
		return nil, err
	}

	return node, nil
}

// UInt64UUID generates an uint64 UUID
func UInt64UUID() (uint64, error) {
	guid, err := uuid.NewRandom()
	if err != nil {
		return 0, err
	}

	bytes := guid[:]
	length := len(bytes)
	return cityhash.CityHash64WithSeeds(bytes, uint32(length), 13329145742295551469, 7926974186468552394), nil
}

// SnowFlakeID generates a snowflake ID. If an error occurs, it logs it and exits.
// Deprecated: Use SafeSnowFlakeID instead.
func SnowFlakeID() string {
	id, err := SafeSnowFlakeID()
	if err != nil {
		log.Fatal(err)
	}
	return id
}

// SafeSnowFlakeID generates a snowflake ID. If an error occurs it returns it.
func SafeSnowFlakeID() (string, error) {
	snowflakeOnce.Do(func() {
		snowflakeNode, snowflakeErr = newSnowFlakeNode()
	})
	if snowflakeErr != nil {
		return "", snowflakeErr
	}
	return snowflakeNode.Generate().String(), nil
}
