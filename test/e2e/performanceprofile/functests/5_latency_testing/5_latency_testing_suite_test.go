package __latency_testing_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/junit"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"

	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

//TODO get commonly used variables from one shared file that defines constants
const testExecutablePath = "../../../../../build/_output/bin/latency-e2e.test"

var prePullNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testing-prepull",
	},
}
var profile *performancev2.PerformanceProfile

var _ = BeforeSuite(func() {
	Expect(isTestExecutableFound()).To(BeTrue())
	Expect(testclient.ClientsEnabled).To(BeTrue())

	// update PP isolated CPUs. the new cpu set for isolated should have an even number of CPUs to avoid failing the pod on SMTAlignment error,
	// and should be greater than what is requested by the test cases in the suite so the test runs properly
	var err error
	profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	workerNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())

	initialIsolated := profile.Spec.CPU.Isolated
	initialReserved := profile.Spec.CPU.Reserved
	//updated both sets to ensure there is no overlap
	latencyIsolatedSet := performancev2.CPUSet("1-9")
	latencyReservedSet := performancev2.CPUSet("0")

	totalCpus := cpuset.MustParse(string(latencyIsolatedSet)).Size() + cpuset.MustParse(string(latencyReservedSet)).Size()
	nodesWithSufficientCpu := nodes.GetByCpuAllocatable(workerNodes, totalCpus)
	//before applying the changes verify that there are compute nodes with sufficient cpus
	if len(nodesWithSufficientCpu) != 0 {
		if *initialIsolated != latencyIsolatedSet || *initialReserved != latencyReservedSet {
			testlog.Info("Update the isolated and reserved cpus sets of the profile")
			err = profilesupdate.UpdateIsolatedReservedCpus(latencyIsolatedSet, latencyReservedSet)
			if err != nil {
				testlog.Error("could not update the profile with the desired CPUs sets")
			}
		}
	}

	if err := createNamespace(); err != nil {
		testlog.Errorf("cannot create the namespace: %v", err)
	}

	ds, err := images.PrePull(testclient.Client, images.Test(), prePullNamespace.Name, "cnf-tests")
	if err != nil {
		data, _ := json.Marshal(ds) // we can safely skip errors
		testlog.Infof("DaemonSet %s/%s image=%q status:\n%s", ds.Namespace, ds.Name, images.Test(), string(data))
		testlog.Errorf("cannot prepull image %q: %v", images.Test(), err)
	}
})

var _ = AfterSuite(func() {
	prePullNamespaceName := prePullNamespace.Name
	err := testclient.Client.Delete(context.TODO(), prePullNamespace)
	if err != nil {
		testlog.Errorf("namespace %q could not be deleted err=%v", prePullNamespace.Name, err)
	}
	namespaces.WaitForDeletion(prePullNamespaceName, 5*time.Minute)

	currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	if reflect.DeepEqual(currentProfile.Spec, profile.Spec) != true {
		testlog.Info("Restore initial performance profile")
		err = profilesupdate.ApplyProfile(profile)
		if err != nil {
			testlog.Errorf("could not restore the initial profile: %v", err)
		}
	}
})

func Test5LatencyTesting(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("latency_testing"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Addon Operator latency tools testing", rr)
}

func createNamespace() error {
	err := testclient.Client.Create(context.TODO(), prePullNamespace)
	if errors.IsAlreadyExists(err) {
		testlog.Warningf("%q namespace already exists, that is unexpected", prePullNamespace.Name)
		return nil
	}
	testlog.Infof("created namespace %q err=%v", prePullNamespace.Name, err)
	return err
}

func isTestExecutableFound() bool {
	if _, err := os.Stat(testExecutablePath); os.IsNotExist(err) {
		return false
	}
	return true
}
