package plane

import "os"

func redisAddr() string { return os.Getenv("SWARM_TEST_REDIS") }
