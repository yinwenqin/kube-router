package service // import "github.com/docker/docker/integration/service"

import (
	"io/ioutil"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	swarmtypes "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/docker/docker/integration/internal/swarm"
	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
	"github.com/gotestyourself/gotestyourself/poll"
	"golang.org/x/net/context"
)

func TestCreateServiceMultipleTimes(t *testing.T) {
	defer setupTest(t)()
	d := swarm.NewSwarm(t, testEnv)
	defer d.Stop(t)
	client, err := client.NewClientWithOpts(client.WithHost((d.Sock())))
	assert.NilError(t, err)

	overlayName := "overlay1"
	networkCreate := types.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "overlay",
	}

	netResp, err := client.NetworkCreate(context.Background(), overlayName, networkCreate)
	assert.NilError(t, err)
	overlayID := netResp.ID

	var instances uint64 = 4
	serviceSpec := swarmServiceSpec("TestService", instances)
	serviceSpec.TaskTemplate.Networks = append(serviceSpec.TaskTemplate.Networks, swarmtypes.NetworkAttachmentConfig{Target: overlayName})

	serviceResp, err := client.ServiceCreate(context.Background(), serviceSpec, types.ServiceCreateOptions{
		QueryRegistry: false,
	})
	assert.NilError(t, err)

	serviceID := serviceResp.ID
	poll.WaitOn(t, serviceRunningTasksCount(client, serviceID, instances), swarm.ServicePoll)

	_, _, err = client.ServiceInspectWithRaw(context.Background(), serviceID, types.ServiceInspectOptions{})
	assert.NilError(t, err)

	err = client.ServiceRemove(context.Background(), serviceID)
	assert.NilError(t, err)

	poll.WaitOn(t, serviceIsRemoved(client, serviceID), swarm.ServicePoll)
	poll.WaitOn(t, noTasks(client), swarm.ServicePoll)

	serviceResp, err = client.ServiceCreate(context.Background(), serviceSpec, types.ServiceCreateOptions{
		QueryRegistry: false,
	})
	assert.NilError(t, err)

	serviceID2 := serviceResp.ID
	poll.WaitOn(t, serviceRunningTasksCount(client, serviceID2, instances), swarm.ServicePoll)

	err = client.ServiceRemove(context.Background(), serviceID2)
	assert.NilError(t, err)

	poll.WaitOn(t, serviceIsRemoved(client, serviceID2), swarm.ServicePoll)
	poll.WaitOn(t, noTasks(client), swarm.ServicePoll)

	err = client.NetworkRemove(context.Background(), overlayID)
	assert.NilError(t, err)

	poll.WaitOn(t, networkIsRemoved(client, overlayID), poll.WithTimeout(1*time.Minute), poll.WithDelay(10*time.Second))
}

func TestCreateWithDuplicateNetworkNames(t *testing.T) {
	defer setupTest(t)()
	d := swarm.NewSwarm(t, testEnv)
	defer d.Stop(t)
	client, err := client.NewClientWithOpts(client.WithHost((d.Sock())))
	assert.NilError(t, err)

	name := "foo"
	networkCreate := types.NetworkCreate{
		CheckDuplicate: false,
		Driver:         "bridge",
	}

	n1, err := client.NetworkCreate(context.Background(), name, networkCreate)
	assert.NilError(t, err)

	n2, err := client.NetworkCreate(context.Background(), name, networkCreate)
	assert.NilError(t, err)

	// Dupliates with name but with different driver
	networkCreate.Driver = "overlay"
	n3, err := client.NetworkCreate(context.Background(), name, networkCreate)
	assert.NilError(t, err)

	// Create Service with the same name
	var instances uint64 = 1
	serviceSpec := swarmServiceSpec("top", instances)

	serviceSpec.TaskTemplate.Networks = append(serviceSpec.TaskTemplate.Networks, swarmtypes.NetworkAttachmentConfig{Target: name})

	service, err := client.ServiceCreate(context.Background(), serviceSpec, types.ServiceCreateOptions{})
	assert.NilError(t, err)

	poll.WaitOn(t, serviceRunningTasksCount(client, service.ID, instances), swarm.ServicePoll)

	resp, _, err := client.ServiceInspectWithRaw(context.Background(), service.ID, types.ServiceInspectOptions{})
	assert.NilError(t, err)
	assert.Check(t, is.Equal(n3.ID, resp.Spec.TaskTemplate.Networks[0].Target))

	// Remove Service
	err = client.ServiceRemove(context.Background(), service.ID)
	assert.NilError(t, err)

	// Make sure task has been destroyed.
	poll.WaitOn(t, serviceIsRemoved(client, service.ID), swarm.ServicePoll)

	// Remove networks
	err = client.NetworkRemove(context.Background(), n3.ID)
	assert.NilError(t, err)

	err = client.NetworkRemove(context.Background(), n2.ID)
	assert.NilError(t, err)

	err = client.NetworkRemove(context.Background(), n1.ID)
	assert.NilError(t, err)

	// Make sure networks have been destroyed.
	poll.WaitOn(t, networkIsRemoved(client, n3.ID), poll.WithTimeout(1*time.Minute), poll.WithDelay(10*time.Second))
	poll.WaitOn(t, networkIsRemoved(client, n2.ID), poll.WithTimeout(1*time.Minute), poll.WithDelay(10*time.Second))
	poll.WaitOn(t, networkIsRemoved(client, n1.ID), poll.WithTimeout(1*time.Minute), poll.WithDelay(10*time.Second))
}

func TestCreateServiceSecretFileMode(t *testing.T) {
	defer setupTest(t)()
	d := swarm.NewSwarm(t, testEnv)
	defer d.Stop(t)
	client, err := client.NewClientWithOpts(client.WithHost((d.Sock())))
	assert.NilError(t, err)

	ctx := context.Background()
	secretResp, err := client.SecretCreate(ctx, swarmtypes.SecretSpec{
		Annotations: swarmtypes.Annotations{
			Name: "TestSecret",
		},
		Data: []byte("TESTSECRET"),
	})
	assert.NilError(t, err)

	var instances uint64 = 1
	serviceSpec := swarmtypes.ServiceSpec{
		Annotations: swarmtypes.Annotations{
			Name: "TestService",
		},
		TaskTemplate: swarmtypes.TaskSpec{
			ContainerSpec: &swarmtypes.ContainerSpec{
				Image:   "busybox:latest",
				Command: []string{"/bin/sh", "-c", "ls -l /etc/secret || /bin/top"},
				Secrets: []*swarmtypes.SecretReference{
					{
						File: &swarmtypes.SecretReferenceFileTarget{
							Name: "/etc/secret",
							UID:  "0",
							GID:  "0",
							Mode: 0777,
						},
						SecretID:   secretResp.ID,
						SecretName: "TestSecret",
					},
				},
			},
		},
		Mode: swarmtypes.ServiceMode{
			Replicated: &swarmtypes.ReplicatedService{
				Replicas: &instances,
			},
		},
	}

	serviceResp, err := client.ServiceCreate(ctx, serviceSpec, types.ServiceCreateOptions{
		QueryRegistry: false,
	})
	assert.NilError(t, err)

	poll.WaitOn(t, serviceRunningTasksCount(client, serviceResp.ID, instances), swarm.ServicePoll)

	filter := filters.NewArgs()
	filter.Add("service", serviceResp.ID)
	tasks, err := client.TaskList(ctx, types.TaskListOptions{
		Filters: filter,
	})
	assert.NilError(t, err)
	assert.Check(t, is.Equal(len(tasks), 1))

	body, err := client.ContainerLogs(ctx, tasks[0].Status.ContainerStatus.ContainerID, types.ContainerLogsOptions{
		ShowStdout: true,
	})
	assert.NilError(t, err)
	defer body.Close()

	content, err := ioutil.ReadAll(body)
	assert.NilError(t, err)
	assert.Check(t, is.Contains(string(content), "-rwxrwxrwx"))

	err = client.ServiceRemove(ctx, serviceResp.ID)
	assert.NilError(t, err)

	poll.WaitOn(t, serviceIsRemoved(client, serviceResp.ID), swarm.ServicePoll)
	poll.WaitOn(t, noTasks(client), swarm.ServicePoll)

	err = client.SecretRemove(ctx, "TestSecret")
	assert.NilError(t, err)
}

func TestCreateServiceConfigFileMode(t *testing.T) {
	defer setupTest(t)()
	d := swarm.NewSwarm(t, testEnv)
	defer d.Stop(t)
	client, err := client.NewClientWithOpts(client.WithHost((d.Sock())))
	assert.NilError(t, err)

	ctx := context.Background()
	configResp, err := client.ConfigCreate(ctx, swarmtypes.ConfigSpec{
		Annotations: swarmtypes.Annotations{
			Name: "TestConfig",
		},
		Data: []byte("TESTCONFIG"),
	})
	assert.NilError(t, err)

	var instances uint64 = 1
	serviceSpec := swarmtypes.ServiceSpec{
		Annotations: swarmtypes.Annotations{
			Name: "TestService",
		},
		TaskTemplate: swarmtypes.TaskSpec{
			ContainerSpec: &swarmtypes.ContainerSpec{
				Image:   "busybox:latest",
				Command: []string{"/bin/sh", "-c", "ls -l /etc/config || /bin/top"},
				Configs: []*swarmtypes.ConfigReference{
					{
						File: &swarmtypes.ConfigReferenceFileTarget{
							Name: "/etc/config",
							UID:  "0",
							GID:  "0",
							Mode: 0777,
						},
						ConfigID:   configResp.ID,
						ConfigName: "TestConfig",
					},
				},
			},
		},
		Mode: swarmtypes.ServiceMode{
			Replicated: &swarmtypes.ReplicatedService{
				Replicas: &instances,
			},
		},
	}

	serviceResp, err := client.ServiceCreate(ctx, serviceSpec, types.ServiceCreateOptions{
		QueryRegistry: false,
	})
	assert.NilError(t, err)

	poll.WaitOn(t, serviceRunningTasksCount(client, serviceResp.ID, instances))

	filter := filters.NewArgs()
	filter.Add("service", serviceResp.ID)
	tasks, err := client.TaskList(ctx, types.TaskListOptions{
		Filters: filter,
	})
	assert.NilError(t, err)
	assert.Check(t, is.Equal(len(tasks), 1))

	body, err := client.ContainerLogs(ctx, tasks[0].Status.ContainerStatus.ContainerID, types.ContainerLogsOptions{
		ShowStdout: true,
	})
	assert.NilError(t, err)
	defer body.Close()

	content, err := ioutil.ReadAll(body)
	assert.NilError(t, err)
	assert.Check(t, is.Contains(string(content), "-rwxrwxrwx"))

	err = client.ServiceRemove(ctx, serviceResp.ID)
	assert.NilError(t, err)

	poll.WaitOn(t, serviceIsRemoved(client, serviceResp.ID))
	poll.WaitOn(t, noTasks(client))

	err = client.ConfigRemove(ctx, "TestConfig")
	assert.NilError(t, err)
}

func swarmServiceSpec(name string, replicas uint64) swarmtypes.ServiceSpec {
	return swarmtypes.ServiceSpec{
		Annotations: swarmtypes.Annotations{
			Name: name,
		},
		TaskTemplate: swarmtypes.TaskSpec{
			ContainerSpec: &swarmtypes.ContainerSpec{
				Image:   "busybox:latest",
				Command: []string{"/bin/top"},
			},
		},
		Mode: swarmtypes.ServiceMode{
			Replicated: &swarmtypes.ReplicatedService{
				Replicas: &replicas,
			},
		},
	}
}

func serviceRunningTasksCount(client client.ServiceAPIClient, serviceID string, instances uint64) func(log poll.LogT) poll.Result {
	return func(log poll.LogT) poll.Result {
		filter := filters.NewArgs()
		filter.Add("service", serviceID)
		tasks, err := client.TaskList(context.Background(), types.TaskListOptions{
			Filters: filter,
		})
		switch {
		case err != nil:
			return poll.Error(err)
		case len(tasks) == int(instances):
			for _, task := range tasks {
				if task.Status.State != swarmtypes.TaskStateRunning {
					return poll.Continue("waiting for tasks to enter run state")
				}
			}
			return poll.Success()
		default:
			return poll.Continue("task count at %d waiting for %d", len(tasks), instances)
		}
	}
}

func noTasks(client client.ServiceAPIClient) func(log poll.LogT) poll.Result {
	return func(log poll.LogT) poll.Result {
		filter := filters.NewArgs()
		tasks, err := client.TaskList(context.Background(), types.TaskListOptions{
			Filters: filter,
		})
		switch {
		case err != nil:
			return poll.Error(err)
		case len(tasks) == 0:
			return poll.Success()
		default:
			return poll.Continue("task count at %d waiting for 0", len(tasks))
		}
	}
}

func serviceIsRemoved(client client.ServiceAPIClient, serviceID string) func(log poll.LogT) poll.Result {
	return func(log poll.LogT) poll.Result {
		filter := filters.NewArgs()
		filter.Add("service", serviceID)
		_, err := client.TaskList(context.Background(), types.TaskListOptions{
			Filters: filter,
		})
		if err == nil {
			return poll.Continue("waiting for service %s to be deleted", serviceID)
		}
		return poll.Success()
	}
}

func networkIsRemoved(client client.NetworkAPIClient, networkID string) func(log poll.LogT) poll.Result {
	return func(log poll.LogT) poll.Result {
		_, err := client.NetworkInspect(context.Background(), networkID, types.NetworkInspectOptions{})
		if err == nil {
			return poll.Continue("waiting for network %s to be removed", networkID)
		}
		return poll.Success()
	}
}
