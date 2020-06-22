<!DOCTYPE html>
<html>
    <head>
        <title>GoHRec Proxy Demo</title>
    </head>
    <body>
        <form action="/" method="POST" target="_self">
            <label>
                Type some text here: <br />
                <textarea name="text"></textarea>
            </label>
            <br />
            <button type="submit">Submit</button>
            <pre><?php echo $_POST['text']; ?></pre>
            <pre><?php print_r(apache_request_headers()); ?></pre>
        </form>
    </body>
</html>
