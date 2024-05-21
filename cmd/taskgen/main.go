/*
Copyright 2022 The Tekton Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"flag"
	tektonapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"strings"
)

const RunnerImage = "quay.io/redhat-appstudio/multi-platform-runner:01c7670e81d5120347cf0ad13372742489985e5f@sha256:246adeaaba600e207131d63a7f706cffdcdc37d8f600c56187123ec62823ff44"

func main() {
	var buildahTask string
	var buildahRemoteTask string
	var taskName string

	flag.StringVar(&buildahTask, "buildah-task", "", "The location of the buildah task")
	flag.StringVar(&buildahRemoteTask, "remote-task", "", "The location of the buildah-remote task to overwrite")
	flag.StringVar(&taskName, "task-name", "", "The task name")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)
	flag.Parse()
	if buildahTask == "" || buildahRemoteTask == "" {
		println("Must specify both buildah-task and remote-task params")
		os.Exit(1)
	}
	if taskName == "" {
		taskName = "buildah-remote"
	}

	task := tektonapi.Task{}
	streamFileYamlToTektonObj(buildahTask, &task)

	decodingScheme := runtime.NewScheme()
	utilruntime.Must(tektonapi.AddToScheme(decodingScheme))
	convertToSsh(&task, taskName)
	y := printers.YAMLPrinter{}
	b := bytes.Buffer{}
	_ = y.PrintObj(&task, &b)
	err := os.WriteFile(buildahRemoteTask, b.Bytes(), 0660)
	if err != nil {
		panic(err)
	}
}

func decodeBytesToTektonObjbytes(bytes []byte, obj runtime.Object) runtime.Object {
	decodingScheme := runtime.NewScheme()
	utilruntime.Must(tektonapi.AddToScheme(decodingScheme))
	decoderCodecFactory := serializer.NewCodecFactory(decodingScheme)
	decoder := decoderCodecFactory.UniversalDecoder(tektonapi.SchemeGroupVersion)
	err := runtime.DecodeInto(decoder, bytes, obj)
	if err != nil {
		panic(err)
	}
	return obj
}

func streamFileYamlToTektonObj(path string, obj runtime.Object) runtime.Object {
	bytes, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		panic(err)
	}
	return decodeBytesToTektonObjbytes(bytes, obj)
}

func convertToSsh(task *tektonapi.Task, taskName string) {

	globalPodmanArgs := ""
	scriptContents := ""
	// Before the build we sync the contents of the workspace to the remote host
	for _, workspace := range task.Spec.Workspaces {
		scriptContents += "\nrsync -ra $(workspaces." + workspace.Name + ".path)/ \"$SSH_HOST:$BUILD_DIR/workspaces/" + workspace.Name + "/\""
		globalPodmanArgs += " -v \"$BUILD_DIR/workspaces/" + workspace.Name + ":$(workspaces." + workspace.Name + ".path):Z\" \\\n"
	}
	for _, volume := range task.Spec.Volumes {
		//TODO: merge all SSH commands on init into a single execution
		scriptContents += "\nssh $SSH_ARGS \"$SSH_HOST\"  mkdir -p \"$BUILD_DIR/volumes/" + volume.Name + "\""
	}
	for _, volumeMount := range task.Spec.StepTemplate.VolumeMounts {
		globalPodmanArgs += " -v \"$BUILD_DIR/volumes/" + volumeMount.Name + ":" + volumeMount.MountPath + ":Z\" \\\n"
	}
	scriptContents += "\nrsync -ra \"$HOME/.docker/\" \"$SSH_HOST:$BUILD_DIR/.docker/\""
	scriptContents += "\nrsync -ra \"/tekton/results/\" \"$SSH_HOST:$BUILD_DIR/tekton-results/\""
	globalPodmanArgs += " -v \"$BUILD_DIR/.docker/:/root/.docker:Z\" \\\n"
	globalPodmanArgs += " -v \"$BUILD_DIR/tekton-results/:/tekton/results:Z\" \\\n"
	globalPodmanArgs += " -v $BUILD_DIR/scripts:/script:Z \\\n"

	for stepPod := range task.Spec.Steps {
		step := &task.Spec.Steps[stepPod]
		ret := `set -o verbose
source /ssh-env/env
/ssh-env/setup.sh
`

		env := "$PODMAN_PORT_FORWARD \\\n"
		script := "scripts/script-" + step.Name + ".sh"

		ret += "\ncat >" + script + " <<'REMOTESSHEOF'\n"
		if !strings.HasPrefix(step.Script, "#!") {
			ret += "#!/bin/bash\nset -o verbose\nset -e\n"
		}
		if step.WorkingDir != "" {
			ret += "cd " + step.WorkingDir + "\n"
		}
		ret += step.Script
		ret += "\nREMOTESSHEOF"
		ret += "\nchmod +x " + script

		if task.Spec.StepTemplate != nil {
			for _, e := range task.Spec.StepTemplate.Env {
				env += " -e " + e.Name + "=\"$" + e.Name + "\" \\\n"
			}
		}
		ret += "\nrsync -ra scripts \"$SSH_HOST:$BUILD_DIR\""
		containerScript := "/script/script-" + step.Name + ".sh"
		for _, e := range step.Env {
			env += " -e " + e.Name + "=\"$" + e.Name + "\" \\\n"
		}
		podmanArgs := globalPodmanArgs

		for _, volumeMount := range step.VolumeMounts {
			podmanArgs += " -v \"$BUILD_DIR/volumes/" + volumeMount.Name + ":" + volumeMount.MountPath + ":Z\" \\\n"
		}
		ret += "\nssh $SSH_ARGS \"$SSH_HOST\" $PORT_FORWARD podman  run " + env + "" + podmanArgs + "--user=0  --rm  \"" + step.Image + "\" " + containerScript

		//sync back results
		ret += "\nrsync -ra \"$SSH_HOST:$BUILD_DIR/tekton-results/\" \"/tekton/results/\""

		for _, i := range strings.Split(ret, "\n") {
			if strings.HasSuffix(i, " ") {
				panic(i)
			}
		}
		step.Script = ret
		step.Image = RunnerImage
		step.VolumeMounts = append(step.VolumeMounts, v1.VolumeMount{
			Name:      "ssh-env",
			ReadOnly:  true,
			MountPath: "/ssh-env",
		})
	}

	task.Name = taskName
	task.Spec.Params = append(task.Spec.Params, tektonapi.ParamSpec{Name: "PLATFORM", Type: tektonapi.ParamTypeString, Description: "The platform to build on"})

	task.Spec.Volumes = append(task.Spec.Volumes, v1.Volume{
		Name: "ssh",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: "multi-platform-ssh-$(context.taskRun.name)",
				Optional:   ref(false),
			},
		},
	})
	task.Spec.Volumes = append(task.Spec.Volumes, v1.Volume{
		Name:         "ssh-env",
		VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}},
	})

	// Add init step, that prepares the remote host, and writes data to the data volume

	script := `set -o verbose

#Retrieve the secret and copy it to the working volume
if [ -e "/ssh/error" ]; then
 #no server could be provisioned
 cat /ssh/error
 exit 1
elif [ -e "/ssh/otp" ]; then
 curl --cacert /ssh/otp-ca -XPOST -d @/ssh/otp $(cat /ssh/otp-server) >/ssh-env/id_rsa
 echo "" >> /ssh-env/id_rsa
else
 cp /ssh/id_rsa /ssh-env/.ssh
fi

#set the permissions
chmod 0400 /ssh-env/id_rsa
cp /ssh-env/id_rsa ~/.ssh/id_rsa

export SSH_HOST=$(cat /ssh/host)
export BUILD_DIR=$(cat /ssh/user-dir)
export SSH_ARGS=""

#initial SSH connection, this both creates some directories we need and creates the known_hosts entry
ssh "-o StrictHostKeyChecking=no" "$SSH_HOST"  mkdir -p "$BUILD_DIR/workspaces" "$BUILD_DIR/scripts"

#copy the known hosts file
cp ~/.ssh/known_hosts /ssh-env/known_hosts

cat >/ssh-env/setup.sh <<EOF
#!/bin/bash
#Create the SSH dir and copy the key with correct permissions
mkdir -p ~/.ssh
cp /ssh-env/id_rsa ~/.ssh/id_rsa
cp /ssh-env/known_hosts ~/.ssh/known_hosts
chmod 0400 ~/.ssh/id_rsa
mkdir -p scripts
EOF
chmod +x /ssh-env/setup.sh
/ssh-env/setup.sh

cat >/ssh-env/env <<EOF
SSH_HOST=$SSH_HOST
BUILD_DIR=$BUILD_DIR
SSH_ARGS=$SSH_ARGS
EOF

if [ -n "$JVM_BUILD_WORKSPACE_ARTIFACT_CACHE_PORT_80_TCP_ADDR" ] ; then
 echo PORT_FORWARD="\" -L 80:$JVM_BUILD_WORKSPACE_ARTIFACT_CACHE_PORT_80_TCP_ADDR:80\"">>/ssh-env/env
 echo PODMAN_PORT_FORWARD="\" -e JVM_BUILD_WORKSPACE_ARTIFACT_CACHE_PORT_80_TCP_ADDR=localhost\"">>/ssh-env/env
else
 echo 'PORT_FORWARD=""'>>/ssh-env/env
 echo 'PODMAN_PORT_FORWARD=""'>>/ssh-env/env
fi

` + scriptContents

	step := tektonapi.Step{
		Name:       "prepare-multi-platform-build",
		Image:      RunnerImage,
		WorkingDir: "",
		VolumeMounts: []v1.VolumeMount{
			{
				Name:      "ssh",
				ReadOnly:  true,
				MountPath: "/ssh",
			},
			{
				Name:      "ssh-env",
				ReadOnly:  false,
				MountPath: "/ssh-env",
			},
		},
		Script: script,
	}
	// Add this initial step to the start
	task.Spec.Steps = append([]tektonapi.Step{step}, task.Spec.Steps...)
}

func ref(val bool) *bool {
	return &val
}
