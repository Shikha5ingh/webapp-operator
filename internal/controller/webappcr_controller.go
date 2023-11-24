/*
Copyright 2023.

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

package controller

import (
	"context"
	"strconv"
	"time"

	kbatch "k8s.io/api/batch/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	// "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	// "sigs.k8s.io/controller-runtime/pkg/source"

	crwebappv1 "webappcr.io/api/v1"
)

// WebappCRReconciler reconciles a WebappCR object
type WebappCRReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=crwebapp.my.domain,resources=webappcrs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crwebapp.my.domain,resources=webappcrs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crwebapp.my.domain,resources=webappcrs/finalizers,verbs=update
//+kubebuilder:rbac:groups=crwebapp.my.domain,resources=cronjobs,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the WebappCR object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *WebappCRReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	webappCR := &crwebappv1.WebappCR{}
	if err := r.Get(ctx, req.NamespacedName, webappCR); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	echoMsg := "echo " + webappCR.Spec.URI

	// Define the desired state of the CronJob based on the WebappCR instance
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webappCR.Name,
			Namespace: webappCR.Namespace,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/1 * * * *", // Example: every 5 minutes
			// we need it from specs of cr

			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(webappCR.Spec.BackoffLimit),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"owner-cronjob": webappCR.Name,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "ubuntu",
									Image: "ubuntu", // Replace with your container image
									Command: []string{
										"/bin/bash",
										"-c",
										"sleep 500000 && " + echoMsg, // Sleep for 60 seconds before running the main command
									},
								},
							},
							RestartPolicy: corev1.RestartPolicyOnFailure,
						},
					},
				},
			},
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &metav1.Time{Time: time.Now()},
		},
	}

	labels := cronJob.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["owner-cronjob"] = webappCR.Name
	cronJob.SetLabels(labels)

	// Set WebappCR instance as the owner and controller
	if err := ctrl.SetControllerReference(webappCR, cronJob, r.Scheme); err != nil {

		return ctrl.Result{}, err
	}

	// Check if there are any remaining child resources
	// if err := r.areChildResourcesDeleted(ctx, cronJob); err != nil {
	// 	// log.Error(err, "error checking child resources")
	// 	return ctrl.Result{}, err
	// }

	// Check if this CronJob already exists
	found := &batchv1.CronJob{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: cronJob.Name, Namespace: cronJob.Namespace}, found)
	if err != nil {
		// If the CronJob does not exist, create it
		if err = r.Client.Create(ctx, cronJob); err != nil {
			return ctrl.Result{}, err
		}
	} else if err == nil {
		// Update the CronJob if it already exists and an update is needed
		// Note: You'll need to determine the logic for when an update is necessary
		cronJob.Spec.JobTemplate.Spec.BackoffLimit = pointer.Int32Ptr(webappCR.Spec.BackoffLimit)
		// Status Update
		if err := r.Client.Status().Update(ctx, cronJob); err != nil {
			// log.Error(err, "unable to update CronJob spec")
			return ctrl.Result{}, err
		}

	}

	cronJobStatus := &batchv1.CronJob{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cronJob), cronJobStatus); err != nil {
		log.Error(err, "unable to get CronJob status")
		return ctrl.Result{}, err
	}

	// Check if the status field is not nil before accessing LastScheduleTime
	if cronJobStatus.Status.LastScheduleTime != nil {
		webappCR.Status.LastExecutionTime = metav1.Time{Time: cronJobStatus.Status.LastScheduleTime.Time}
	} else {
		log.Info("CronJob status is nil")
		// Handle the case where the status is nil, log an error, or take appropriate action
	}

	// execute status

	var childJobs kbatch.JobList
	var activeJobs []*kbatch.Job

	if err := r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingLabels(labels)); err != nil {
		log.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}
	log.Info(strconv.Itoa(len(childJobs.Items)))
	// log.Info(labels["owner-cronjob"])

	isJobFinished := func(job *kbatch.Job) (bool, kbatch.JobConditionType) {
		for _, c := range job.Status.Conditions {

			if (c.Type == kbatch.JobComplete || c.Type == kbatch.JobFailed) && c.Status == corev1.ConditionTrue {
				return true, c.Type
			}
		}

		return false, ""
	}

	for i, job := range childJobs.Items {
		finished, _ := isJobFinished(&job)
		// log.Info(job.Name)
		if !finished {
			activeJobs = append(activeJobs, &childJobs.Items[i])
		}
	}

	if len(activeJobs) > 0 {
		webappCR.Status.ExecutionStatus = "Active"
		r.Status().Update(ctx, webappCR)
	} else {
		webappCR.Status.ExecutionStatus = "Inactive"
		r.Status().Update(ctx, webappCR)

	}

	if err := r.Status().Update(ctx, webappCR); err != nil {
		log.Error(err, "unable to update WebappCR status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WebappCRReconciler) reconcileJobs(ctx context.Context, webappCR *crwebappv1.WebappCR) error {
	// Your logic to reconcile jobs based on the WebappCR
	// ...
	// webappCR := &crwebappv1.WebappCR{}
	log := log.FromContext(ctx)
	log.Info("here")

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebappCRReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crwebappv1.WebappCR{}).
		Owns(&batchv1.CronJob{}).
		// Watches(
		// 	&source.Kind{Type: &batchv1.CronJob{}},
		// 	&handler.EnqueueRequestForOwner{
		// 		IsController: true,
		// 		OwnerType:    &crwebappv1.WebappCR{},
		// 	},
		// ).
		Complete(r)
}
