//%attributes = {}
#DECLARE($worker : 4D:C1709.SystemWorker; $params : Object)

var $text : Text
$text:=Replace string:C233($worker.responseError; "<stdio>"; "stdio"; *)  //information
ALERT:C41($text)

var $data : 4D:C1709.Blob
$data:=$worker.response  ////the distilled pdf
$file:=$params.context
$file.setContent($data)