property stdOut : 4D:C1709.Blob
property stdErr : Text

Class extends _CLI_Controller

Class constructor($CLI : cs:C1710._CLI)
	
	Super:C1705($CLI)
	
	This:C1470.clear()
	
Function clear() : cs:C1710._distill_Controller
	
	This:C1470.stdOut:=4D:C1709.Blob.new()
	This:C1470.stdErr:=""
	
	return This:C1470
	
Function onData($worker : 4D:C1709.SystemWorker; $params : Object)
	
	var $instance : cs:C1710.distill
	$instance:=This:C1470.instance
	
	If (OB Instance of:C1731($instance.onData; 4D:C1709.Function))
		$instance.onData.call($worker; $params)
	End if 
	
Function onDataError($worker : 4D:C1709.SystemWorker; $params : Object)
	
	var $instance : cs:C1710.distill
	$instance:=This:C1470.instance
	
	If (OB Instance of:C1731($instance.onDataError; 4D:C1709.Function))
		$instance.onDataError.call($worker; $params)
	End if 
	
Function onResponse($worker : 4D:C1709.SystemWorker; $params : Object)
	
	var $instance : cs:C1710.distill
	$instance:=This:C1470.instance
	
	If (OB Instance of:C1731($instance.onResponse; 4D:C1709.Function))
		$instance.onResponse.call(This:C1470; $worker; $params)
	Else 
		This:C1470.stdOut:=$worker.response
		This:C1470.stdErr:=$worker.responseError
	End if 
	
Function onError($worker : 4D:C1709.SystemWorker; $params : Object)
	
	var $instance : cs:C1710.distill
	$instance:=This:C1470.instance
	
	If (OB Instance of:C1731($instance.onError; 4D:C1709.Function))
		$instance.onError.call($worker; $params)
	Else 
		This:C1470.stdErr:=$worker.responseError
	End if 
	
Function onTerminate($worker : 4D:C1709.SystemWorker; $params : Object)
	
	var $instance : cs:C1710.distill
	$instance:=This:C1470.instance
	
	If (OB Instance of:C1731($instance.onTerminate; 4D:C1709.Function))
		$instance.onTerminate.call($worker; $params)
	End if 