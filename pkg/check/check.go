package check

import (
	"context"
	"fmt"
	"sort"

	"github.com/segmentio/topicctl/pkg/admin"
	"github.com/segmentio/topicctl/pkg/config"
	tconfig "github.com/segmentio/topicctl/pkg/config"
)

// CheckConfig contains all of the context necessary to check a single topic config.
type CheckConfig struct {
	AdminClient   admin.Client
	ClusterConfig config.ClusterConfig
	CheckLeaders  bool
	NumRacks      int
	TopicConfig   config.TopicConfig
	ValidateOnly  bool
}

// CheckTopic runs the topic check and returns a result. If there's a non-topic-specific error
// (e.g., cluster zk isn't reachable), then an error is returned.
func CheckTopic(ctx context.Context, config CheckConfig) (TopicCheckResults, error) {
	results := TopicCheckResults{}
	brokers, err := config.AdminClient.GetBrokers(ctx, nil)
	if err != nil {
		return results, err
	}

	// Check config
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNameConfigCorrect,
		},
	)
	if err := config.TopicConfig.Validate(config.NumRacks); err == nil {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			fmt.Sprintf("config validation error: %+v", err),
		)
		// Don't bother with remaining checks
		return results, nil
	}

	// Check topic/cluster consistency
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNameConfigsConsistent,
		},
	)
	if err := tconfig.CheckConsistency(config.TopicConfig, config.ClusterConfig); err == nil {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			fmt.Sprintf("config consistency error error: %+v", err),
		)
		// Don't bother with remaining checks
		return results, nil
	}

	if config.ValidateOnly {
		return results, nil
	}

	// Check existence
	// results.AppendResult(
	// 	TopicCheckResult{
	// 		Name: CheckNameTopicExists,
	// 	},
	// )
    topicDoesNotExist := false
	topicInfo, err := config.AdminClient.GetTopic(ctx, config.TopicConfig.Meta.Name, true)
	if err != nil {
	// Don't bother with remaining checks if we can't get the topic
		if err == admin.ErrTopicDoesNotExist {
			topicDoesNotExist = true
		}
	// results.UpdateLastResult(false, "")
	// return results, nil
	}

	// return results, err
	// }
	// results.UpdateLastResult(true, "")

	// skip CheckNameConfigSettingsCorrect if topic does not exist
	if !topicDoesNotExist {
		// Check retention
		results.AppendResult(
			TopicCheckResult{
				Name: CheckNameConfigSettingsCorrect,
			},
		)

		settings := config.TopicConfig.Spec.Settings.Copy()
		if config.TopicConfig.Spec.RetentionMinutes > 0 {
			settings[admin.RetentionKey] = config.TopicConfig.Spec.RetentionMinutes * 60000
		}

		diffKeys, missingKeys, err := settings.ConfigMapDiffs(topicInfo.Config)
		if err != nil {
			return results, err
		}

		if len(diffKeys) == 0 && len(missingKeys) == 0 {
			results.UpdateLastResult(true, "")
		} else {
			combinedKeys := []string{}
			for _, diffKey := range diffKeys {
				combinedKeys = append(combinedKeys, diffKey)
			}
			for _, missingKey := range missingKeys {
				combinedKeys = append(combinedKeys, missingKey)
			}

			sort.Slice(combinedKeys, func(a, b int) bool {
				return combinedKeys[a] < combinedKeys[b]
			})

			results.UpdateLastResult(
				false,
				fmt.Sprintf(
					"%d keys have different values between cluster and topic config: %v",
					len(combinedKeys),
					combinedKeys,
				),
			)
		}
	}
	// Check replication factor
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNameReplicationFactorCorrect,
		},
	)

	if config.TopicConfig.Spec.ReplicationFactor%len(brokers) == 0 {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			fmt.Sprintf(
				"len(ReplicationFactor) %d must be a multiple of len(broker) %d",
				config.TopicConfig.Spec.ReplicationFactor,
				len(brokers),
			),
		)
	}

	// Check partitions
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNamePartitionCountCorrect,
		},
	)
	if config.TopicConfig.Spec.Partitions%len(brokers) == 0 || config.TopicConfig.Spec.Partitions == 1 {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			fmt.Sprintf(
				"len(Partitions) %d must be a multiple of len(broker) %d",
				config.TopicConfig.Spec.Partitions,
				len(brokers),
			),
		)
	}

	// Check throttles
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNameThrottlesClear,
		},
	)
	if !topicInfo.IsThrottled() {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			"topic has existing throttles",
		)
	}

	// Check replicas in-sync
	results.AppendResult(
		TopicCheckResult{
			Name: CheckNameReplicasInSync,
		},
	)
	outOfSyncPartitions := topicInfo.OutOfSyncPartitions(nil)

	if len(outOfSyncPartitions) == 0 {
		results.UpdateLastResult(true, "")
	} else {
		results.UpdateLastResult(
			false,
			fmt.Sprintf(
				"%d/%d partitions have out-of-sync replicas",
				len(outOfSyncPartitions),
				len(topicInfo.Partitions),
			),
		)
	}

	// Check leaders
	if config.CheckLeaders {
		results.AppendResult(
			TopicCheckResult{
				Name: CheckNameLeadersCorrect,
			},
		)
		wrongLeaderPartitions := topicInfo.WrongLeaderPartitions(nil)

		if len(wrongLeaderPartitions) == 0 {
			results.UpdateLastResult(true, "")
		} else {
			results.UpdateLastResult(
				false,
				fmt.Sprintf(
					"%d/%d partitions have wrong leaders",
					len(wrongLeaderPartitions),
					len(topicInfo.Partitions),
				),
			)
		}
	}

	return results, nil
}
