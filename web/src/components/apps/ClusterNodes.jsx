import React, { Component, Fragment } from "react";
import classNames from "classnames";
import dayjs from "dayjs";
import { withRouter } from "react-router-dom";
import { Helmet } from "react-helmet";
import CodeSnippet from "../shared/CodeSnippet";
import NodeRow from "./NodeRow";
import Loader from "../shared/Loader";
import { rbacRoles } from "../../constants/rbac";
import { Utilities } from "../../utilities/utilities";
import { Repeater } from "../../utilities/repeater";
import ErrorModal from "../modals/ErrorModal";
import Modal from "react-modal";

import "@src/scss/components/apps/ClusterNodes.scss";

export class ClusterNodes extends Component {
  state = {
    generating: false,
    command: "",
    expiry: null,
    displayAddNode: false,
    selectedNodeType: "secondary", // Change when primary node script is enabled
    generateCommandErrMsg: "",
    kurl: null,
    getNodeStatusJob: new Repeater(),
    deletNodeError: "",
    confirmDeleteNode: "",
    showConfirmDrainModal: false,
    nodeNameToDrain: "",
    drainingNodeName: null,
    drainNodeSuccessful: false
  }

  componentDidMount() {
    this.getNodeStatus();
    this.state.getNodeStatusJob.start(this.getNodeStatus, 1000);
  }

  componentWillUnmount() {
    this.state.getNodeStatusJob.stop();
  }

  getNodeStatus = async () => {
    try {
      const res = await fetch(`${window.env.API_ENDPOINT}/kurl/nodes`, {
        headers: {
          "Authorization": Utilities.getToken(),
          "Accept": "application/json",
        },
        method: "GET",
      });
      if (!res.ok) {
        if (res.status === 401) {
          Utilities.logoutUser();
          return;
        }
        console.log("failed to get node status list, unexpected status code", res.status);
        return;
      }
      const response = await res.json();
      this.setState({
        kurl: response,
      });
      return response;
    } catch(err) {
      console.log(err);
      throw err;
    }
  }

  deleteNode = (name) => {
    this.setState({
      confirmDeleteNode: name,
    });
  }

  cancelDeleteNode = () => {
    this.setState({
      confirmDeleteNode: "",
    });
  }

  reallyDeleteNode = () => {
    const name = this.state.confirmDeleteNode;
    this.cancelDeleteNode();

    fetch(`${window.env.API_ENDPOINT}/kurl/nodes/${name}`, {
      headers: {
        "Authorization": Utilities.getToken(),
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      method: "DELETE",
    })
      .then(async (res) => {
        if (!res.ok) {
          if (res.status === 401) {
            Utilities.logoutUser();
            return;
          }
          if (res.status === 422) {
            this.setState({
              deleteNodeError: "The ekco add-on is required to delete nodes but was not found in your cluster. https://kurl.sh/docs/add-ons/ekco",
            });
            return;
          }
          this.setState({
            deleteNodeError: `Delete failed with status ${res.status}`,
          });
        }
      })
      .catch((err) => {
        console.log(err);
      })
  }

  generateWorkerAddNodeCommand = async () => {
    this.setState({ generating: true, command: "", expiry: null, generateCommandErrMsg: "" });

    fetch(`${window.env.API_ENDPOINT}/kurl/generate-node-join-command-secondary`, {
      headers: {
        "Authorization": Utilities.getToken(),
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      method: "POST",
    })
      .then(async (res) => {
        const data = await res.json();
        this.setState({ generating: false, command: data.command, expiry: data.expiry });
      })
      .catch((err) => {
        console.log(err);
        this.setState({
          generating: false,
          generateCommandErrMsg: err ? err.message : "Something went wrong",
        });
      });
  }

  onDrainNodeClick = (name) => {
    this.setState({
      showConfirmDrainModal: true,
      nodeNameToDrain: name
    });
  }

  drainNode = async (name) => {
    this.setState({ showConfirmDrainModal: false, drainingNodeName: name });
    fetch(`${window.env.API_ENDPOINT}/kurl/nodes/${name}/drain`, {
      headers: {
        "Authorization": Utilities.getToken(),
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      method: "POST",
    })
      .then(async (res) => {
        this.setState({ drainNodeSuccessful: true });
        setTimeout(() => {
          this.setState({
            drainingNodeName: null,
            drainNodeSuccessful: false
          });
        }, 3000);
      })
      .catch((err) => {
        console.log(err);
        this.setState({
          drainingNodeName: null,
          drainNodeSuccessful: false
        });
      })
  }

  generatePrimaryAddNodeCommand = async () => {
    this.setState({ generating: true, command: "", expiry: null, generateCommandErrMsg: "" });

    fetch(`${window.env.API_ENDPOINT}/kurl/generate-node-join-command-primary`, {
      headers: {
        "Authorization": Utilities.getToken(),
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      method: "POST",
    })
      .then(async (res) => {
        const data = await res.json();
        this.setState({ generating: false, command: data.command, expiry: data.expiry });
      })
      .catch((err) => {
        console.log(err);
        this.setState({
          generating: false,
          generateCommandErrMsg: err ? err.message : "Something went wrong",
        });
      });
  }

  onAddNodeClick = () => {
    this.setState({
      displayAddNode: true
    }, async () => {
      await this.generateWorkerAddNodeCommand();
    });
  }

  onSelectNodeType = event => {
    const value = event.currentTarget.value;
    this.setState({
      selectedNodeType: value
    }, async () => {
      if (this.state.selectedNodeType === "secondary") {
        await this.generateWorkerAddNodeCommand();
      } else {
        await this.generatePrimaryAddNodeCommand();
      }
    });
  }

  ackDeleteNodeError = () => {
    this.setState({ deleteNodeError: "" });
  }

  render() {
    const { kurl } = this.state;
    const { displayAddNode, generateCommandErrMsg } = this.state;

    if (!kurl) {
      return (
        <div className="flex-column flex1 alignItems--center justifyContent--center">
          <Loader size="60" />
        </div>
      );
    }
    return (
      <div className="ClusterNodes--wrapper container flex-column flex1 u-overflow--auto u-paddingTop--50">
        <Helmet>
          <title>{`${this.props.appName ? `${this.props.appName} Cluster Management` : "Cluster Management"}`}</title>
        </Helmet>
        <div className="flex-column flex1 alignItems--center">
          <div className="flex1 flex-column centered-container">
            <div className="u-paddingBottom--30">
              <p className="flex-auto u-fontSize--larger u-fontWeight--bold u-textColor--primary u-paddingBottom--10">Your nodes</p>
              <div className="flex1 u-overflow--auto">
                {kurl?.nodes && kurl?.nodes.map((node, i) => (
                  <NodeRow
                    key={i}
                    node={node}
                    drainingNodeName={this.state.drainingNodeName}
                    drainNodeSuccessful={this.state.drainNodeSuccessful}
                    drainNode={kurl?.isKurlEnabled ? this.onDrainNodeClick : null}
                    deleteNode={kurl?.isKurlEnabled ? this.deleteNode : null} />
                ))}
              </div>
            </div>
            {kurl?.isKurlEnabled && Utilities.sessionRolesHasOneOf([rbacRoles.CLUSTER_ADMIN]) ?
              !displayAddNode
                ? (
                  <div className="flex justifyContent--center alignItems--center">
                    <button className="btn primary" onClick={this.onAddNodeClick}>Add a node</button>
                  </div>
                )
                : (
                  <div className="flex-column">
                    <div>
                      <p className="u-width--full u-fontSize--larger u-textColor--primary u-fontWeight--bold u-lineHeight--normal u-borderBottom--gray u-paddingBottom--10">
                        Add a Node
                      </p>
                    </div>
                    <div className="flex justifyContent--center alignItems--center u-marginTop--15">
                      <div className={classNames("BoxedCheckbox flex-auto flex u-marginRight--20", {
                        "is-active": this.state.selectedNodeType === "primary",
                        "is-disabled": !kurl?.ha
                      })}>
                        <input
                          id="primaryNode"
                          className="u-cursor--pointer hidden-input"
                          type="radio"
                          name="nodeType"
                          value="primary"
                          disabled={!kurl?.ha}
                          checked={this.state.selectedNodeType === "primary"}
                          onChange={this.onSelectNodeType}
                        />
                        <label
                          htmlFor="primaryNode"
                          className="flex1 flex u-width--full u-position--relative u-cursor--pointer u-userSelect--none">
                          <div className="flex-auto">
                            <span className="icon clickable commitOptionIcon u-marginRight--10" />
                          </div>
                          <div className="flex1">
                            <p className="u-textColor--primary u-fontSize--normal u-fontWeight--medium">Primary Node</p>
                            <p className="u-textColor--bodyCopy u-lineHeight--normal u-fontSize--small u-fontWeight--medium u-marginTop--5">Provides high availability</p>
                          </div>
                        </label>
                      </div>
                      <div className={classNames("BoxedCheckbox flex-auto flex u-marginRight--20", {
                        "is-active": this.state.selectedNodeType === "secondary"
                      })}>
                        <input
                          id="secondaryNode"
                          className="u-cursor--pointer hidden-input"
                          type="radio"
                          name="nodeType"
                          value="secondary"
                          checked={this.state.selectedNodeType === "secondary"}
                          onChange={this.onSelectNodeType}
                        />
                        <label htmlFor="secondaryNode" className="flex1 flex u-width--full u-position--relative u-cursor--pointer u-userSelect--none">
                          <div className="flex-auto">
                            <span className="icon clickable commitOptionIcon u-marginRight--10" />
                          </div>
                          <div className="flex1">
                            <p className="u-textColor--primary u-fontSize--normal u-fontWeight--medium">Secondary Node</p>
                            <p className="u-textColor--bodyCopy u-lineHeight--normal u-fontSize--small u-fontWeight--medium u-marginTop--5">Optimal for running application workloads</p>
                          </div>
                        </label>
                      </div>
                    </div>
                    {this.state.generating && (
                      <div className="flex u-width--full justifyContent--center">
                        <Loader size={60} />
                      </div>
                    )}
                    {!this.state.generating && this.state.command?.length > 0
                      ? (
                        <Fragment>
                          <p className="u-fontSize--normal u-textColor--bodyCopy u-fontWeight--medium u-lineHeight--normal u-marginBottom--5 u-marginTop--15">
                            Run this command on the node you wish to join the cluster
                          </p>
                          <CodeSnippet
                            language="bash"
                            canCopy={true}
                            onCopyText={<span className="u-textColor--success">Command has been copied to your clipboard</span>}
                          >
                            {[this.state.command.join(" \\\n  ")]}
                          </CodeSnippet>
                          {this.state.expiry && (
                            <span className="timestamp u-marginTop--15 u-width--full u-textAlign--right u-fontSize--small u-fontWeight--bold u-textColor--primary">
                              {`Expires on ${dayjs(this.state.expiry).format("MMM Do YYYY, h:mm:ss a z")} UTC${ -1 * (new Date().getTimezoneOffset()) / 60}`}
                            </span>
                          )}
                        </Fragment>
                      )
                      : (
                        <Fragment>
                          {generateCommandErrMsg &&
                            <div className="alignSelf--center u-marginTop--15">
                              <span className="u-textColor--error">{generateCommandErrMsg}</span>
                            </div>
                          }
                        </Fragment>
                      )
                    }
                  </div>
                )
            : null}
          </div>
        </div>
        {this.state.deleteNodeError &&
          <ErrorModal
            errorModal={true}
            toggleErrorModal={this.ackDeleteNodeError}
            err={"Failed to delete node"}
            errMsg={this.state.deleteNodeError}
          />
        }
        <Modal
          isOpen={!!this.state.confirmDeleteNode}
          onRequestClose={this.cancelDeleteNode}
          shouldReturnFocusAfterClose={false}
          contentLabel="Confirm Delete Node"
          ariaHideApp={false}
          className="Modal"
        >
          <div className="Modal-body">
            <p className="u-fontSize--normal u-textColor--bodyCopy u-lineHeight--normal u-marginBottom--20">
              Deleting this node may cause data loss. Are you sure you want to proceed?
            </p>
            <div className="u-marginTop--10 flex">
              <button
                onClick={this.reallyDeleteNode}
                type="button"
                className="btn red primary"
              >
                Delete {this.state.confirmDeleteNode}
              </button>
              <button
                onClick={this.cancelDeleteNode}
                type="button"
                className="btn secondary u-marginLeft--20"
              >
                Cancel
              </button>
            </div>
          </div>
        </Modal>
        {this.state.showConfirmDrainModal &&
          <Modal
            isOpen={true}
            onRequestClose={() => this.setState({ showConfirmDrainModal: false, nodeNameToDrain: "" })}
            shouldReturnFocusAfterClose={false}
            contentLabel="Confirm Drain Node"
            ariaHideApp={false}
            className="Modal MediumSize"
          >
            <div className="Modal-body">
              <p className="u-fontSize--larger u-textColor--primary u-fontWeight--bold u-lineHeight--normal">
                Are you sure you want to drain {this.state.nodeNameToDrain}?
              </p>
              <p className="u-fontSize--normal u-textColor--bodyCopy u-lineHeight--normal u-marginBottom--20">
                Draining this node may cause data loss. If you want to delete {this.state.nodeNameToDrain} you must disconnect it after it has been drained.
              </p>
              <div className="u-marginTop--10 flex">
                <button
                  onClick={() => this.drainNode(this.state.nodeNameToDrain)}
                  type="button"
                  className="btn red primary"
                >
                  Drain {this.state.nodeNameToDrain}
                </button>
                <button
                  onClick={() => this.setState({ showConfirmDrainModal: false, nodeNameToDrain: "" })}
                  type="button"
                  className="btn secondary u-marginLeft--20"
                >
                  Cancel
                </button>
              </div>
            </div>
          </Modal>
        }
      </div>
    );
  }
}

export default withRouter(ClusterNodes);
