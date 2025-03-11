package util

import (
	"log"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"github.com/zentures/cityhash"
)

var (
	snowflakeNode *snowflake.Node
)

// init Initialize snowflake node.
func init() {
	ip, err := GetFirstPrivateIP()
	if err != nil {
		ip, err = GetFirstIP()
		if err != nil {
			log.Fatal(err)
		}
	}

	id := IPtoUInt(ip)
	snowflakeNode, err = snowflake.NewNode(int64(id % 1024))
	if err != nil {
		log.Fatal(err)
	}
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

// SnowFlakeID generates a snowflake ID
func SnowFlakeID() string {
	return snowflakeNode.Generate().String()
}
