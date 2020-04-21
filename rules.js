function OnSignerStartup() {
}

function OnApprovedTx() {

}

function ApproveListing(req) {
  return "Approve"
}

function ApproveTx(req) {
  var transaction = req.transaction

  if(transaction.value !== "0x0") {
    return "Reject"
  }

  return "Approve"
}

function ApproveSignData(req) {
  return "Approve"
}